// Copyright 2015 ThoughtWorks, Inc.

// This file is part of Gauge.

// Gauge is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// Gauge is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.

// You should have received a copy of the GNU General Public License
// along with Gauge.  If not, see <http://www.gnu.org/licenses/>.

package api

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/getgauge/common"
	"github.com/getgauge/gauge/api/infoGatherer"
	"github.com/getgauge/gauge/conn"
	"github.com/getgauge/gauge/gauge_messages"
	"github.com/getgauge/gauge/logger"
	"github.com/getgauge/gauge/manifest"
	"github.com/getgauge/gauge/reporter"
	"github.com/getgauge/gauge/runner"
	"github.com/getgauge/gauge/stream"
	"github.com/getgauge/gauge/util"
)

// StartAPI calls StartAPIService and returns the channels
func StartAPI() *runner.StartChannels {
	startChan := &runner.StartChannels{RunnerChan: make(chan *runner.TestRunner), ErrorChan: make(chan error), KillChan: make(chan bool)}
	go StartAPIService(0, 0, startChan)
	return startChan
}

// StartAPIService starts the Gauge API service
func StartAPIService(port, v2port int, startChannels *runner.StartChannels) {
	specInfoGatherer := new(infoGatherer.SpecInfoGatherer)
	apiHandler := &gaugeAPIMessageHandler{specInfoGatherer: specInfoGatherer}
	gch, err := conn.NewGaugeConnectionHandler(port, v2port, apiHandler)
	if err != nil {
		startChannels.ErrorChan <- fmt.Errorf("Connection error. %s", err.Error())
		return
	}
	setAPIPort := func(value int, envVariable string) {
		if port == 0 {
			if err := common.SetEnvVariable(envVariable, strconv.Itoa(value)); err != nil {
				startChannels.ErrorChan <- fmt.Errorf("Failed to set Env variable %s. %s", envVariable, err.Error())
			}
		}
		return
	}

	setAPIPort(gch.ConnectionPortNumber(), common.APIPortEnvVariableName)
	setAPIPort(gch.APIV2PortNumber(), common.APIV2PortEnvVariableName)

	go gch.HandleMultipleConnections()

	gauge_messages.RegisterExecutionServer(gch.GRPCServer, &stream.ExecutionStream{})
	go gch.ServeGRPCServer()

	runner, err := connectToRunner(startChannels.KillChan)
	if err != nil {
		startChannels.ErrorChan <- err
		return
	}
	specInfoGatherer.MakeListOfAvailableSteps(runner)
	startChannels.RunnerChan <- runner
}

func connectToRunner(killChannel chan bool) (*runner.TestRunner, error) {
	manifest, err := manifest.ProjectManifest()
	if err != nil {
		return nil, err
	}

	runner, connErr := runner.StartRunnerAndMakeConnection(manifest, reporter.Current(), killChannel)
	if connErr != nil {
		return nil, connErr
	}

	return runner, nil
}

func runAPIServiceIndefinitely(port, v2port int) {
	startChan := &runner.StartChannels{RunnerChan: make(chan *runner.TestRunner), ErrorChan: make(chan error), KillChan: make(chan bool)}
	go StartAPIService(port, v2port, startChan)
	go checkParentIsAlive(startChan)

	for {
		select {
		case runner := <-startChan.RunnerChan:
			logger.Info("Got a kill message. Killing runner.")
			runner.Kill()
		case err := <-startChan.ErrorChan:
			logger.Fatalf("Killing Gauge daemon. %v", err.Error())
		}
	}
}

func checkParentIsAlive(startChannels *runner.StartChannels) {
	parentProcessID := os.Getppid()
	for {
		if !util.IsProcessRunning(parentProcessID) {
			startChannels.ErrorChan <- fmt.Errorf("Parent process with pid %d has terminated.", parentProcessID)
			return
		}
		time.Sleep(1 * time.Second)
	}
}

// RunInBackground runs Gauge in daemonized mode on the given apiPort
func RunInBackground(apiPort, apiV2Port string) {
	runAPIServiceIndefinitely(getValidAPIPort(apiPort, common.APIPortEnvVariableName), getValidAPIPort(apiV2Port, common.APIV2PortEnvVariableName))
}

func getValidAPIPort(p, e string) int {
	var port int
	var err error
	if p != "" {
		port, err = strconv.Atoi(p)
		if err != nil {
			logger.Fatalf(fmt.Sprintf("Invalid port number: %s", p))
		}
		os.Setenv(e, p)
	} else {
		port, err = conn.GetPortFromEnvironmentVariable(e)
		if err != nil {
			logger.Fatalf(fmt.Sprintf("Failed to start API Service. %s \n", err.Error()))
		}
	}
	return port
}

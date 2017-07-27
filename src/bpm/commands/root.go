// Copyright (C) 2017-Present Pivotal Software, Inc. All rights reserved.
//
// This program and the accompanying materials are made available under
// the terms of the under the Apache License, Version 2.0 (the "License”);
// you may not use this file except in compliance with the License.
//
// You may obtain a copy of the License at
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
// WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.  See the
// License for the specific language governing permissions and limitations
// under the License.

package commands

import (
	"bpm/bpm"
	"bpm/runc/adapter"
	"bpm/runc/client"
	"bpm/runc/lifecycle"
	"bpm/usertools"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"code.cloudfoundry.org/clock"
	"code.cloudfoundry.org/lager"
	"code.cloudfoundry.org/lager/lagerflags"

	"github.com/spf13/cobra"
)

var jobName, processName, configPath string
var logger lager.Logger

var userFinder = usertools.NewUserFinder()

var RootCmd = &cobra.Command{
	Long:          "A bosh process manager for starting and stopping release jobs",
	RunE:          root,
	Short:         "A bosh process manager for starting and stopping release jobs",
	SilenceErrors: true,
	Use:           "bpm",
	ValidArgs:     []string{"start", "stop", "list"},
}

func root(cmd *cobra.Command, args []string) error {
	return errors.New("Exit code 1")
}

func validateInput(args []string) error {
	if len(args) < 1 {
		return errors.New("must specify a job")
	}

	jobName = args[0]

	if processName == "" {
		processName = jobName
	}

	configPath = filepath.Join(
		bpm.BoshRoot(),
		"jobs", jobName, "config", "bpm",
		fmt.Sprintf("%s.yml", processName),
	)

	return nil
}

func setupBpmLogs(sessionName string) error {
	bpmLogFileLocation := filepath.Join(bpm.BoshRoot(), "sys", "log", jobName, "bpm.log")
	err := os.MkdirAll(filepath.Join(bpm.BoshRoot(), "sys", "log", jobName), 0750)
	if err != nil {
		return err
	}

	logFile, err := os.OpenFile(bpmLogFileLocation, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}

	usr, err := userFinder.Lookup(usertools.VcapUser)
	if err != nil {
		return err
	}

	err = os.Chown(bpmLogFileLocation, int(usr.UID), int(usr.GID))
	if err != nil {
		return err
	}

	logger, _ = lagerflags.NewFromConfig("bpm", lagerflags.DefaultLagerConfig())
	logger.RegisterSink(lager.NewWriterSink(logFile, lager.INFO))
	logger = logger.WithData(lager.Data{"job": jobName, "process": processName})
	logger = logger.Session(sessionName)

	return nil
}

func acquireLifecycleLock() error {
	l := logger.Session("acquiring-lifecycle-lock")
	l.Info("starting")
	defer l.Info("complete")

	bpmPidDir := filepath.Join(bpm.BoshRoot(), "sys", "run", "bpm", jobName)
	err := os.MkdirAll(bpmPidDir, 0700)
	if err != nil {
		l.Error("failed-to-create-lock-dir", err)
		return err
	}

	lockFile := filepath.Join(bpmPidDir, fmt.Sprintf("%s.lock", processName))
	f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		l.Error("failed-to-create-lock-file", err)
		return err
	}

	err = unix.Flock(int(f.Fd()), unix.LOCK_EX)
	if err != nil {
		l.Error("failed-to-acquire-lock", err)
		return err
	}

	return nil
}

func releaseLifecycleLock() error {
	l := logger.Session("releasing-lifecycle-lock")
	l.Info("starting")
	defer l.Info("complete")

	lockFile := filepath.Join(bpm.BoshRoot(), "sys", "run", "bpm", jobName, fmt.Sprintf("%s.lock", processName))
	err := os.RemoveAll(lockFile)
	if err != nil {
		l.Error("failed-to-remove-lock-file", err)
		return err
	}

	return nil
}

func newRuncLifecycle() *lifecycle.RuncLifecycle {
	runcClient := client.NewRuncClient(bpm.RuncPath(), bpm.RuncRoot())
	runcAdapter := adapter.NewRuncAdapter()
	clock := clock.NewClock()

	return lifecycle.NewRuncLifecycle(
		runcClient,
		runcAdapter,
		userFinder,
		lifecycle.NewCommandRunner(),
		clock,
		bpm.BoshRoot(),
	)
}
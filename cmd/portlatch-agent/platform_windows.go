// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/portlatch/portlatch-agent/internal/agent"
	"github.com/portlatch/portlatch-agent/internal/state"
)

const (
	serviceName    = "portlatch-agent"
	serviceDisplay = "Portlatch agent"

	logFile    = "agent.log"
	logMaxSize = 10 << 20
)

const usage = `Usage, from an administrator PowerShell:

  portlatch-agent.exe enrol       Approve this machine, then install and start the service
  portlatch-agent.exe uninstall   Stop and remove the service, with the agent's key and token

Without a command, the agent runs in this window until you close it.
`

// platformMain runs the service when Windows starts it, and the two commands
// that install and remove it. It returns false to run in the foreground.
func platformMain(cfg agent.Config) bool {
	if isService, err := svc.IsWindowsService(); err == nil && isService {
		runService(cfg)
		return true
	}
	if len(os.Args) < 2 {
		return false
	}
	switch os.Args[1] {
	case "enrol":
		os.Exit(enrolCommand(cfg))
	case "uninstall":
		os.Exit(uninstallCommand(cfg))
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	return true
}

// enrolCommand shows the code in this window, waits for the approval, then
// hands the enrolled agent over to a service that starts with Windows.
func enrolCommand(cfg agent.Config) int {
	setUpLogging(os.Stderr, cfg.LogLevel)

	manager, err := mgr.Connect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Run this command from an administrator PowerShell (%v).\n", err)
		return 1
	}
	defer manager.Disconnect()

	if existing, err := manager.OpenService(serviceName); err == nil {
		existing.Close()
		fmt.Fprintln(os.Stderr, "The Portlatch agent service is already installed. To start over: portlatch-agent.exe uninstall")
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := agent.Enrol(ctx, cfg); err != nil {
		if ctx.Err() == nil {
			slog.Error("enrolment failed", "error", err)
		}
		return 1
	}

	// The service runs this very file: it must stay where it is.
	exe, err := os.Executable()
	if err != nil {
		slog.Error("could not locate the executable", "error", err)
		return 1
	}
	service, err := manager.CreateService(serviceName, exe, mgr.Config{
		DisplayName: serviceDisplay,
		Description: "Keeps the WireGuard tunnel to Portlatch up, and relays your routes to this network.",
		StartType:   mgr.StartAutomatic,
	})
	if err != nil {
		slog.Error("could not install the service", "error", err)
		return 1
	}
	defer service.Close()

	// Restarted after a failure, but not after a clean stop: a deleted agent
	// stops cleanly, and restarting it would change nothing.
	restart := mgr.RecoveryAction{Type: mgr.ServiceRestart, Delay: 10 * time.Second}
	if err := service.SetRecoveryActions([]mgr.RecoveryAction{restart, restart, restart}, uint32((24 * time.Hour).Seconds())); err != nil {
		slog.Warn("could not set the restart policy", "error", err)
	}
	if err := service.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		slog.Warn("could not set the restart policy", "error", err)
	}

	if err := service.Start(); err != nil {
		slog.Error("the service is installed but did not start", "error", err)
		return 1
	}

	fmt.Printf("\nThe Portlatch agent is installed and running. It starts with Windows.\nLogs: %s\n", filepath.Join(cfg.DataDir, logFile))
	return 0
}

// uninstallCommand stops and removes the service, then the key and the token:
// the agent can no longer connect, and is left to delete from the dashboard.
func uninstallCommand(cfg agent.Config) int {
	manager, err := mgr.Connect()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Run this command from an administrator PowerShell (%v).\n", err)
		return 1
	}
	defer manager.Disconnect()

	if service, err := manager.OpenService(serviceName); err == nil {
		if err := stopService(service); err != nil {
			fmt.Fprintf(os.Stderr, "Could not stop the service: %v\n", err)
			service.Close()
			return 1
		}
		if err := service.Delete(); err != nil {
			fmt.Fprintf(os.Stderr, "Could not remove the service: %v\n", err)
			service.Close()
			return 1
		}
		service.Close()
	} else {
		fmt.Println("No Portlatch agent service was installed.")
	}

	store, err := state.Open(cfg.DataDir)
	if err == nil {
		err = store.Forget()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "The service is removed, but not the agent's key and token: %v\n", err)
		return 1
	}

	fmt.Printf("The Portlatch agent is removed. Delete it from your dashboard too: https://portlatch.eu/dashboard\nIts logs stay in %s.\n", cfg.DataDir)
	return 0
}

func stopService(service *mgr.Service) error {
	status, err := service.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		return nil
	}
	if _, err := service.Control(svc.Stop); err != nil {
		return err
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		if status, err = service.Query(); err != nil {
			return err
		}
		if status.State == svc.Stopped {
			return nil
		}
	}
	return errors.New("still running after 30 seconds")
}

func runService(cfg agent.Config) {
	logs, err := openRotatingFile(filepath.Join(cfg.DataDir, logFile), logMaxSize)
	if err == nil {
		defer logs.Close()
		setUpLogging(logs, cfg.LogLevel)
	}
	if err := svc.Run(serviceName, &service{cfg: cfg}); err != nil {
		slog.Error("the service could not run", "error", err)
		os.Exit(1)
	}
}

type service struct {
	cfg agent.Config
}

func (s *service) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx, s.cfg) }()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	for {
		select {
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				status <- request.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			}
		case err := <-done:
			if err == nil {
				return false, 0
			}
			slog.Error("agent stopped", "error", err)
			// A clean stop for a deleted agent, so the recovery actions leave it be.
			if errors.Is(err, agent.ErrRejected) {
				return false, 0
			}
			return false, 1
		}
	}
}

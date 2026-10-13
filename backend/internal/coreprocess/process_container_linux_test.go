//go:build linux

package coreprocess

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

func TestContainerPidfdSignalFixture(t *testing.T) {
	if os.Getenv("GUANGYUE_PIDFD_FIXTURE") != "1" {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	fmt.Fprintln(os.Stdout, "ready")
	<-ctx.Done()
	os.Exit(0)
}

func TestContainerRestartPidfdRejectsChangedGenerationAndSignalsOnlyVerifiedProcess(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestContainerPidfdSignalFixture$")
	cmd.Env = append(os.Environ(), "GUANGYUE_PIDFD_FIXTURE=1")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	ready := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(pipe)
		ready <- scanner.Scan() && scanner.Text() == "ready"
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("fixture did not start")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("fixture startup timed out")
	}
	previous := containerGeneration{cmd.Process.Pid, "123", "xray"}
	if err = signalContainerGeneration(previous, func() (containerGeneration, error) {
		return containerGeneration{previous.PID, "124", "xray"}, nil
	}); err == nil {
		t.Fatal("changed process generation was signalled")
	}
	if err = syscall.Kill(cmd.Process.Pid, 0); err != nil {
		t.Fatal("identity rejection killed the process")
	}
	if err = signalContainerGeneration(previous, func() (containerGeneration, error) { return previous, nil }); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("verified process did not receive graceful TERM:", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("pidfd did not signal the intended process")
	}
}

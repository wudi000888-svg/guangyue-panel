package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
)

func runtimeServiceCommand(name string) (string, []string, error) {
	switch name {
	case "panel":
		return "/opt/guangyue-personal/bin/guangyue", []string{"-config", "/etc/guangyue-personal.json"}, nil
	case "xray":
		return "/opt/guangyue-personal/bin/xray", []string{"run", "-config", "/var/lib/guangyue/xray.json"}, nil
	case "hy2":
		return "/opt/guangyue-personal/bin/hysteria", []string{"server", "--disable-update-check", "-c", "/var/lib/guangyue/hy2.json"}, nil
	default:
		return "", nil, errors.New("unknown managed service")
	}
}

// Exactly one child lifetime per systemd MainPID. Exiting children terminate
// the wrapper, allowing systemd to restart with a new accounting generation.
func superviseRuntimeChild(ctx context.Context, cmd *exec.Cmd, dir string, out, errOut io.Writer) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout = &runtimeLogWriter{dir: dir, out: out}
	cmd.Stderr = &runtimeLogWriter{dir: dir, out: errOut}
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// Signal the whole child group, including the panel's managed bridge.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		timer := time.NewTimer(12 * time.Second)
		defer timer.Stop()
		select {
		case err := <-done:
			return err
		case <-timer.C:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			return <-done
		}
	}
}

func runRuntimeService(name string) int {
	path, args, err := runtimeServiceCommand(name)
	if err != nil {
		return 2
	}
	// The child keeps the existing service's memory/concurrency environment;
	// these limits apply only to the small output/signal wrapper itself.
	debug.SetMemoryLimit(16 << 20)
	runtime.GOMAXPROCS(1)
	cmd := exec.Command(path, args...)
	cmd.Env = []string{}
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GUANGYUE_LOG_WRAPPER=") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "GUANGYUE_LOG_WRAPPER="+name)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = superviseRuntimeChild(ctx, cmd, "/var/lib/guangyue", os.Stdout, os.Stderr)
	if err == nil || ctx.Err() != nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() > 0 {
		return exit.ExitCode()
	}
	return 1
}

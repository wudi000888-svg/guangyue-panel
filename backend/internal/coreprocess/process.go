// Package coreprocess contains bounded OS adapters for managed proxy services.
package coreprocess

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func Command(timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}
func PID(unit string) int {
	b, err := Command(3*time.Second, "systemctl", "show", "--property=MainPID", "--value", unit)
	if err != nil {
		return 0
	}
	id, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return id
}
func Restart(unit string) error {
	pid := PID(unit)
	if pid < 2 {
		return errors.New("core is not running; systemd recovery required")
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err = p.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	// Signalling the wrapper is asynchronous. Its old API port can remain
	// reachable until the child exits; callers must not mistake that for the
	// replacement being ready and repeatedly terminate the recovering service.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return waitReplacement(ctx, pid, func() int { return PID(unit) })
}

func waitReplacement(ctx context.Context, previous int, current func() int) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if pid := current(); pid > 1 && pid != previous {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("core replacement did not start; systemd recovery required")
		case <-ticker.C:
		}
	}
}

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
	return p.Signal(syscall.SIGTERM)
}

//go:build linux

package coreprocess

import (
	"errors"

	"golang.org/x/sys/unix"
)

func signalContainerGeneration(previous containerGeneration, current func() (containerGeneration, error)) error {
	fd, err := unix.PidfdOpen(previous.PID, 0)
	if err != nil {
		return errors.New("container core requires pidfd support; Linux kernel 5.4 or newer is required")
	}
	defer unix.Close(fd)
	// The fd belongs to this kernel process, not a number that can be reused.
	if now, e := current(); e != nil || now != previous {
		return errors.New("container core generation changed; retry operation")
	}
	if err = unix.PidfdSendSignal(fd, unix.SIGTERM, nil, 0); err != nil && !errors.Is(err, unix.ESRCH) {
		return errors.New("container core could not be signalled")
	}
	return nil
}

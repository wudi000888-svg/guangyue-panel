//go:build !linux

package coreprocess

import "errors"

func signalContainerGeneration(containerGeneration, func() (containerGeneration, error)) error {
	return errors.New("container runtime requires Linux")
}

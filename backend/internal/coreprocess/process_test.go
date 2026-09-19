package coreprocess

import (
	"context"
	"testing"
	"time"
)

func TestRestartWaitsForReplacementGeneration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pids := []int{123, 0, 124}
	reads := 0
	err := waitReplacement(ctx, 123, func() int {
		pid := pids[min(reads, len(pids)-1)]
		reads++
		return pid
	})
	if err != nil || reads != 3 {
		t.Fatalf("acknowledged old or stopped core: reads=%d, err=%v", reads, err)
	}
}

func TestRestartCannotAcknowledgeUnrecoveredService(t *testing.T) {
	for _, pid := range []int{0, 1, 123} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		err := waitReplacement(ctx, 123, func() int { return pid })
		cancel()
		if err == nil {
			t.Fatalf("acknowledged unavailable replacement %d", pid)
		}
	}
}

package controlplane

import (
	"math"
	"testing"
)

func TestProrateIsExactWithoutOverflow(t *testing.T) {
	const tb = int64(1) << 40
	for _, c := range []struct{ amount, part, whole, want int64 }{
		{1000, 1, 2, 500},
		{99900, 50 * tb, 100 * tb, 49950}, // 100 TB plan: amount*part overflows int64
		{100_000_000_000, 1 << 59, 1 << 60, 50_000_000_000},
		{math.MaxInt64, math.MaxInt64 - 1, math.MaxInt64, math.MaxInt64 - 1},
		{1000, 3, 0, 0},
		{1000, -1, 10, 0},
		{1000, 20, 10, 1000}, // part is capped at whole
		{1, 1, 3, 0},
	} {
		if got := prorate(c.amount, c.part, c.whole); got != c.want {
			t.Errorf("prorate(%d,%d,%d)=%d want %d", c.amount, c.part, c.whole, got, c.want)
		}
	}
}

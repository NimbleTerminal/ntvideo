package ntvideo

import (
	"math"
	"testing"
	"time"
)

func TestMediaDuration(t *testing.T) {
	for _, seconds := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1, math.MaxFloat64, float64(math.MaxInt64) / 1e9} {
		if got := mediaDuration(seconds); got != 0 {
			t.Fatalf("mediaDuration(%v) = %v", seconds, got)
		}
	}
	if got := mediaDuration(1.25); got != 1250*time.Millisecond {
		t.Fatal(got)
	}
}

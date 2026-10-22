package ntvideo

import (
	"math"
	"time"
)

// Invalid or unrepresentable media timestamps are unknown, not seekable.
func mediaDuration(seconds float64) time.Duration {
	nanos := seconds * float64(time.Second)
	if math.IsNaN(nanos) || math.IsInf(nanos, 0) || nanos < 0 || nanos >= float64(math.MaxInt64) {
		return 0
	}
	return time.Duration(nanos)
}

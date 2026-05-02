package ratelimit

import "time"

// tokenBucketDecide implements the standard refilling-bucket algorithm.
// The bucket capacity is burst (>= limit). Tokens refill at rate
// limit/window per second. A successful admission deducts one token.
//
// The first observation initializes the bucket full, so the first burst
// of requests up to capacity is admitted regardless of clock skew.
func tokenBucketDecide(e *entry, now time.Time, limit, burst int, window time.Duration) (admit bool, remaining int, reset time.Time) {
	capacity := float64(burst)
	rate := float64(limit) / window.Seconds()

	if e.last.IsZero() {
		e.tokens = capacity
		e.last = now
	} else {
		elapsed := now.Sub(e.last).Seconds()
		if elapsed > 0 {
			e.tokens += elapsed * rate
			if e.tokens > capacity {
				e.tokens = capacity
			}
			e.last = now
		}
	}

	if e.tokens >= 1 {
		e.tokens -= 1
		admit = true
	}
	remaining = int(e.tokens)
	if remaining < 0 {
		remaining = 0
	}

	// Reset = time at which the bucket reaches full (or, when admitted,
	// the time at which one more token is available — close enough for
	// the X-RateLimit-Reset header which is informational).
	if rate > 0 {
		secsToFull := (capacity - e.tokens) / rate
		reset = now.Add(time.Duration(secsToFull * float64(time.Second)))
	} else {
		reset = now
	}
	return
}

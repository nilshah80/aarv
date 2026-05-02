package ratelimit

import "time"

// slidingWindowDecide implements a fixed-resolution sliding-window
// counter. The window is divided into slidingBuckets sub-buckets covering
// adjacent slices of wall time; the total request count within the window
// is the sum of all sub-buckets in the ring.
//
// # Why absolute sub-bucket indexing
//
// We index each sample by its absolute sub-window number on the wall
// clock — `nowAbsSub = floor(now / subWindow)` — and map to the ring via
// `nowAbsSub % slidingBuckets`. When a new sample arrives at a different
// absSub than the previous one, the buckets in the gap (from the previous
// sample's slot, exclusive, to the new sample's slot, inclusive) are
// zeroed: they have rolled out of the window. Buckets we did NOT cross
// retain their counts so a request from earlier in the same window still
// counts against the limit.
//
// An earlier rotation-based formulation cleared the current bucket as
// soon as one sub-window had elapsed, which let an old request fall out
// of the count well before the configured Window — fixed here.
func slidingWindowDecide(e *entry, now time.Time, limit int, window time.Duration) (admit bool, remaining int, reset time.Time) {
	subWindow := window / slidingBuckets
	if subWindow <= 0 {
		// Pathologically small window (< slidingBuckets ns). Treat the
		// whole window as a single bucket; not a realistic config.
		subWindow = window
		if subWindow <= 0 {
			subWindow = time.Nanosecond
		}
	}

	nowAbsSub := now.UnixNano() / int64(subWindow)

	// Brand-new entries have lastAbsSub == 0 and zero buckets. The gap
	// branch below sees gap >= slidingBuckets and zeroes the (already
	// zero) ring before re-anchoring — safe.
	gap := nowAbsSub - e.lastAbsSub
	if gap > 0 {
		if gap >= slidingBuckets {
			for i := range e.buckets {
				e.buckets[i] = 0
			}
		} else {
			// Zero the buckets that have rolled out: the indices
			// (lastAbsSub, nowAbsSub] mod slidingBuckets.
			for i := int64(1); i <= gap; i++ {
				idx := int((e.lastAbsSub + i) % slidingBuckets)
				e.buckets[idx] = 0
			}
		}
		e.lastAbsSub = nowAbsSub
	}

	idx := int(nowAbsSub % slidingBuckets)

	total := 0
	for _, c := range e.buckets {
		total += c
	}

	if total < limit {
		e.buckets[idx]++
		total++
		admit = true
	}
	remaining = limit - total
	if remaining < 0 {
		remaining = 0
	}

	// Reset = end of the current sub-bucket (informational only — the
	// sliding window has no single hard reset point).
	reset = time.Unix(0, (nowAbsSub+1)*int64(subWindow))
	return
}

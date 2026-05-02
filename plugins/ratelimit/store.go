package ratelimit

import (
	"sync"
	"sync/atomic"
	"time"
)

// shardCount is the fixed number of mutex-sharded buckets in the store.
// 64 keeps per-shard contention low under realistic load while bounding
// per-shard overhead (one mutex + one map). Power of two so & masking is
// a valid alternative to %; we use Go's runtime hash which is stable
// enough across versions for this use.
const shardCount = 64

// store is a sharded keyed-state map shared by token-bucket and
// sliding-window algorithms. Per-shard mutexes cap contention without the
// global-mutex hot spot a single sync.Map would otherwise have under
// concurrent same-key writes (sync.Map is read-heavy).
type store struct {
	shards [shardCount]storeShard

	// sweepCounter drives deterministic lazy cleanup. Every shardCount-th
	// limiter check (admitted OR denied) triggers a single-shard sweep.
	// atomic.Uint64 avoids math/rand cost and is reproducible in tests.
	// Counting denied requests too means a flood of denied traffic also
	// drives cleanup of unrelated stale keys.
	sweepCounter atomic.Uint64

	entryTTL time.Duration
}

type storeShard struct {
	mu sync.Mutex
	m  map[string]*entry
}

// entry stores per-key state. The variant fields (token-bucket vs
// sliding-window) are unioned into the same struct so the store can be
// algorithm-agnostic — only the configured algorithm reads/writes its
// own fields.
type entry struct {
	lastAccess time.Time

	// Token bucket fields.
	tokens float64
	last   time.Time

	// Sliding window fields. lastAbsSub is the absolute sub-window
	// index (now.UnixNano() / subWindow) of the most recent sample. On a
	// brand-new entry it is 0 and the gap-based clear path in
	// slidingWindowDecide safely zeroes the (already-zero) buckets and
	// re-anchors.
	lastAbsSub int64
	buckets    [slidingBuckets]int
}

// slidingBuckets controls the resolution of the sliding-window algorithm.
// 10 sub-buckets per window is a common precision/memory tradeoff.
const slidingBuckets = 10

func newStore(entryTTL time.Duration) *store {
	s := &store{entryTTL: entryTTL}
	for i := range s.shards {
		s.shards[i].m = make(map[string]*entry)
	}
	return s
}

// shardFor returns the shard responsible for key.
func (s *store) shardFor(key string) *storeShard {
	return &s.shards[fnvHash(key)%shardCount]
}

// fnvHash is FNV-1a 64-bit. Inlined here to keep the dependency surface
// at zero (no hash/fnv import — saves a few ns and aligns with the rest
// of plugins/* avoiding hash imports).
func fnvHash(s string) uint64 {
	const offset = 14695981039346656037
	const prime = 1099511628211
	var h uint64 = offset
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

// withEntry runs fn under the shard lock, creating the entry if absent.
// The sweepCounter is incremented on every call (not just admissions) so
// that denied requests also drive cleanup — under sustained denial
// pressure, a key that goes silent should still age out without needing
// a janitor goroutine.
func (s *store) withEntry(key string, fn func(*entry)) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	e, ok := sh.m[key]
	if !ok {
		e = &entry{}
		sh.m[key] = e
	}
	fn(e)
	e.lastAccess = time.Now()
	sh.mu.Unlock()

	// Deterministic sweep: every shardCount-th call triggers a sweep on
	// one shard. Cycles through all shards every shardCount*shardCount
	// calls; under steady load each shard is swept roughly once per
	// shardCount² calls — bounded work, no goroutine.
	if c := s.sweepCounter.Add(1); c%shardCount == 0 {
		s.sweepShard(int((c / shardCount) % shardCount))
	}
}

// sweepShard removes entries whose lastAccess is older than entryTTL.
// Bounded work per call: walks one shard's map.
func (s *store) sweepShard(idx int) {
	if s.entryTTL <= 0 {
		return
	}
	sh := &s.shards[idx]
	cutoff := time.Now().Add(-s.entryTTL)
	sh.mu.Lock()
	for k, e := range sh.m {
		if e.lastAccess.Before(cutoff) {
			delete(sh.m, k)
		}
	}
	sh.mu.Unlock()
}

// sweepAll runs sweepShard on every shard. Used by the NewWithCleanup
// janitor goroutine and by tests asserting eviction.
func (s *store) sweepAll() {
	for i := range s.shards {
		s.sweepShard(i)
	}
}

// size returns the total number of entries across all shards. Test-only;
// the store does not export it.
func (s *store) size() int {
	total := 0
	for i := range s.shards {
		s.shards[i].mu.Lock()
		total += len(s.shards[i].m)
		s.shards[i].mu.Unlock()
	}
	return total
}

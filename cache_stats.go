package gallery

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// cacheStats is the snapshot of the cache state that visitors
// read when rendering the page. Replaced atomically every 30
// sec by the stats-refresh goroutine. The visitor code never
// blocks on the eviction goroutine — they just load the
// latest snapshot pointer and read fields.
//
// All numeric fields are computed by the snapshot function
// from the in-memory events list + a single walk of the
// cache directory.
//
// Per user request 2026-06-27: the footer shows four hex
// values:
//
//	XX  //  YY  //  ZZ  //  AA
//
// Where:
//
//	XX  = cache usage percent, 00-FF
//	      (or a special marker for unbounded — see below)
//	YY  = peak evictions in any 1-hour bucket in the last
//	      24 hours, 00-FF (clamped to 255)
//	ZZ  = peak evictions in any 1-hour bucket in the last
//	      7 days, 00-FF
//	AA  = peak evictions in any 1-hour bucket in the last
//	      4 weeks (28 days), 00-FF
//
// When MaxCacheSizeMB is 0 (no cap), XX is rendered as the
// infinity symbol (∞) and YY/ZZ/AA are always 00 (no
// eviction can happen).
type cacheStats struct {
	// SizeBytes is the total bytes used by the on-disk thumb
	// cache. Refreshed every 30 sec by the stats refresh
	// goroutine (a single os.ReadDir + os.Stat walk).
	SizeBytes int64

	// FileCount is the number of thumb files in the cache.
	FileCount int64

	// CapBytes is the configured cap (0 = unbounded). Used to
	// compute the cache usage percent. Same value as
	// Gallery.MaxCacheSizeMB but converted to bytes here for
	// template convenience.
	CapBytes int64

	// PeakEvictions24h is the max number of evictions in any
	// single 1-hour bucket within the last 24 hours. Clamped
	// to 255 (0xFF) so the hex display is always two digits.
	PeakEvictions24h int

	// PeakEvictions7d is the max within the last 7 days.
	PeakEvictions7d int

	// PeakEvictions28d is the max within the last 4 weeks.
	PeakEvictions28d int
}

// CacheUsagePercent returns the cache usage as an integer
// 0-100 (or -1 if the cap is disabled). Used by the footer
// template to compute XX.
func (s *cacheStats) CacheUsagePercent() int {
	if s == nil || s.CapBytes <= 0 {
		return -1 // unbounded — caller renders ∞
	}
	pct := int(s.SizeBytes * 100 / s.CapBytes)
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	return pct
}

// evictionEvent is one hour's worth of eviction activity.
// Stored in a sorted slice (oldest first) on the tracker.
// Pruned to 28 days on every snapshot.
type evictionEvent struct {
	// HourStart is the unix-second timestamp of the start
	// of the hour (UTC). Truncated to the hour boundary so
	// events in the same hour have the same HourStart and
	// merge together.
	HourStart int64

	// Count is the number of evictions in this hour. Multiple
	// eviction runs in the same hour accumulate to this
	// number.
	Count int
}

// cacheStatsTracker holds the eviction history for one
// Gallery instance. Two goroutines touch it:
//
//   - the eviction goroutine (calls recordEvictions)
//   - the stats-refresh goroutine (calls snapshot)
//
// Both run in the Caddy process (not per-request). They are
// serialised by a single mutex (mu). The atomic.Pointer
// gives visitors lock-free reads of the latest snapshot.
type cacheStatsTracker struct {
	// events is the append-only log of eviction events.
	// Sorted by HourStart ascending. Pruned to 28 days on
	// every snapshot (events older than 28 days can never
	// appear in any peak calculation).
	mu     sync.Mutex
	events []evictionEvent

	// latest is the snapshot pointer. Atomic swap on every
	// stats-refresh tick (30 sec). Visitors do
	// atomic.LoadPointer to read the current snapshot
	// without locking.
	latest atomic.Pointer[cacheStats]
}

// newCacheStatsTracker returns a fresh tracker with an
// initial empty snapshot (SizeBytes=0, FileCount=0, peaks=0,
// CapBytes set from the gallery's MaxCacheSizeMB).
func newCacheStatsTracker(capMB int) *cacheStatsTracker {
	t := &cacheStatsTracker{}
	initial := &cacheStats{
		SizeBytes:        0,
		FileCount:        0,
		CapBytes:         int64(capMB) * 1024 * 1024,
		PeakEvictions24h: 0,
		PeakEvictions7d:  0,
		PeakEvictions28d: 0,
	}
	t.latest.Store(initial)
	return t
}

// recordEvictions appends (or merges into) an eviction
// event for the current hour. Called from evictIfOver after
// a successful eviction run. Safe to call concurrently with
// snapshot — mu serialises both.
func (t *cacheStatsTracker) recordEvictions(count int, at time.Time) {
	if t == nil || count <= 0 {
		return
	}
	hourStart := at.Truncate(time.Hour).Unix()
	t.mu.Lock()
	defer t.mu.Unlock()
	// Merge into the last event if it's the same hour (the
	// typical case — most hours have 0 or 1 eviction runs).
	if n := len(t.events); n > 0 && t.events[n-1].HourStart == hourStart {
		t.events[n-1].Count += count
		return
	}
	// Otherwise append a new event.
	t.events = append(t.events, evictionEvent{
		HourStart: hourStart,
		Count:     count,
	})
}

// snapshot computes a fresh cacheStats from the in-memory
// events + a walk of the cache directory, then atomically
// swaps it into the latest pointer. Returns the new
// snapshot (caller doesn't need it but it's useful for
// testing).
//
// Cost: one os.ReadDir + one os.Stat per file in the cache.
// For a 1 GB cache with ~5,000 thumbs, that's ~50 ms on
// an SSD. Called every 30 sec by the stats-refresh
// goroutine — negligible.
//
// When MaxCacheSizeMB is 0 (no cap), the directory walk
// still happens (we want to show the current size and
// file count) but CapBytes is 0, which causes the XX
// rendering to show ∞.
func (t *cacheStatsTracker) snapshot(cacheDir string, capMB int) *cacheStats {
	if t == nil {
		return nil
	}
	now := time.Now().UTC()
	hourNow := now.Truncate(time.Hour).Unix()

	// Prune events older than 28 days and merge same-hour
	// events into single buckets. The events slice is
	// already sorted ascending, so we can scan from the
	// start and drop anything older than (now - 28 days).
	cutoff28d := now.Add(-28 * 24 * time.Hour).Unix()
	t.mu.Lock()
	// The events slice is APPEND-ordered, not sorted (because
	// tests pass arbitrary timestamps). Sort it now so the
	// pruning loop and the peak scan both work correctly.
	// In production, append-order matches chronological
	// order (recordEvictions is only called with time.Now()).
	sort.Slice(t.events, func(i, j int) bool {
		return t.events[i].HourStart < t.events[j].HourStart
	})
	// Drop oldest events (those with HourStart before cutoff)
	i := 0
	for i < len(t.events) && t.events[i].HourStart < cutoff28d {
		i++
	}
	if i > 0 {
		t.events = t.events[i:]
	}
	// Take a snapshot of the events under the lock
	events := make([]evictionEvent, len(t.events))
	copy(events, t.events)
	t.mu.Unlock()

	// Compute the three peaks. We always walk the whole
	// events slice (max 672 hours = 4 weeks) and use a
	// min(peak, 255) clamp so the hex display is always
	// two digits (0xFF).
	peak24h, peak7d, peak28d := 0, 0, 0
	cutoff24h := now.Add(-24 * time.Hour).Unix()
	cutoff7d := now.Add(-7 * 24 * time.Hour).Unix()
	for _, ev := range events {
		// Skip events that haven't happened yet (clock skew).
		if ev.HourStart > hourNow {
			continue
		}
		c := ev.Count
		if c > 255 {
			c = 255 // clamp for hex display
		}
		if ev.HourStart >= cutoff24h && c > peak24h {
			peak24h = c
		}
		if ev.HourStart >= cutoff7d && c > peak7d {
			peak7d = c
		}
		if c > peak28d {
			peak28d = c
		}
	}

	// Walk the cache directory to get current size + count.
	var sizeBytes int64
	var fileCount int64
	// Per user request 2026-06-30: the cache uses a 2-level
	// nested hash layout (<cacheDir>/<aa>/<bb>/<rest>.webp).
	// We recurse into nested subdirs with filepath.Walk so
	// the size + file count includes everything in the nested
	// tree. We also count both thumbs (.webp / .jpg / .png)
	// AND sidecars (.meta / .exif) toward fileCount for
	// accurate reporting.
	filepath.Walk(cacheDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if info.IsDir() {
			return nil // directories don't count toward size
		}
		sizeBytes += info.Size()
		fileCount++
		return nil
	})

	s := &cacheStats{
		SizeBytes:        sizeBytes,
		FileCount:        fileCount,
		CapBytes:         int64(capMB) * 1024 * 1024,
		PeakEvictions24h: peak24h,
		PeakEvictions7d:  peak7d,
		PeakEvictions28d: peak28d,
	}
	t.latest.Store(s)
	return s
}

// load returns the most recent snapshot without blocking.
// Safe to call from any goroutine (the visitor's render
// path). The returned pointer is to an immutable snapshot —
// callers must not mutate it.
func (t *cacheStatsTracker) load() *cacheStats {
	if t == nil {
		return nil
	}
	return t.latest.Load()
}

// formatCacheStatsFooter formats a cacheStats snapshot into
// the four hex strings displayed in the footer:
//   - XX: cache usage percent (00-FF) or "∞" if unbounded
//   - YY: peak evictions in any 1-hour bucket in last 24h
//   - ZZ: peak evictions in any 1-hour bucket in last 7d
//   - AA: peak evictions in any 1-hour bucket in last 28d
//
// Per user request 2026-06-27. Clamped to 0xFF so the hex
// is always two digits. Nil stats (e.g. before the first
// refresh tick) renders as "00 // 00 // 00 // ∞" (or "00
// // 00 // 00 // 00" if there's no cap — wait, the XX
// case is independent of stats being nil).
func formatCacheStatsFooter(stats *cacheStats) (xx, yy, zz, aa string) {
	if stats == nil {
		return "00", "00", "00", "00"
	}
	pct := stats.CacheUsagePercent()
	if pct < 0 {
		// Unbounded — XX is the infinity symbol
		xx = "∞"
	} else {
		xx = fmt.Sprintf("%02X", pct)
	}
	yy = fmt.Sprintf("%02X", clampInt255(stats.PeakEvictions24h))
	zz = fmt.Sprintf("%02X", clampInt255(stats.PeakEvictions7d))
	aa = fmt.Sprintf("%02X", clampInt255(stats.PeakEvictions28d))
	return xx, yy, zz, aa
}

// clampInt255 clamps an int to [0, 255] for the hex display.
func clampInt255(n int) int {
	if n < 0 {
		return 0
	}
	if n > 255 {
		return 255
	}
	return n
}

package gallery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCacheStats_InitialState verifies a freshly-created
// tracker returns all-zero stats (no size, no file count,
// no peaks, default cap).
func TestCacheStats_InitialState(t *testing.T) {
	tracker := newCacheStatsTracker(1024)
	stats := tracker.load()
	if stats == nil {
		t.Fatal("expected non-nil stats from new tracker")
	}
	if stats.SizeBytes != 0 {
		t.Errorf("expected SizeBytes=0, got %d", stats.SizeBytes)
	}
	if stats.FileCount != 0 {
		t.Errorf("expected FileCount=0, got %d", stats.FileCount)
	}
	if stats.CapBytes != 1024*1024*1024 {
		t.Errorf("expected CapBytes=1GB, got %d", stats.CapBytes)
	}
	if stats.CacheUsageFractionHex255() != 0 {
		t.Errorf("expected CacheUsageFractionHex255=0, got %d", stats.CacheUsageFractionHex255())
	}
	if stats.PeakEvictions24h != 0 || stats.PeakEvictions7d != 0 || stats.PeakEvictions28d != 0 {
		t.Errorf("expected all peaks=0, got 24h=%d 7d=%d 28d=%d",
			stats.PeakEvictions24h, stats.PeakEvictions7d, stats.PeakEvictions28d)
	}
}

// TestCacheStats_UnboundedCap verifies that with CapBytes=0
// (unbounded), CacheUsageFractionHex255 returns -1.
func TestCacheStats_UnboundedCap(t *testing.T) {
	tracker := newCacheStatsTracker(0)
	stats := tracker.load()
	if stats.CapBytes != 0 {
		t.Errorf("expected CapBytes=0, got %d", stats.CapBytes)
	}
	if stats.CacheUsageFractionHex255() != -1 {
		t.Errorf("expected CacheUsageFractionHex255=-1 for unbounded, got %d", stats.CacheUsageFractionHex255())
	}
}

// TestCacheStats_RecordEvictions verifies that recordEvictions
// increments the run count by 1 per call (regardless of the
// value passed in). Per user request 2026-07-04: the YY/ZZ/AA
// hex values in the cache-status footer represent the number
// of eviction RUNS, not the number of files evicted. A single
// run that evicts 50 files counts as 1.
func TestCacheStats_RecordEvictions(t *testing.T) {
	tmp := t.TempDir()
	tracker := newCacheStatsTracker(1024)

	// One call to recordEvictions (regardless of the
	// count parameter) = 1 run in the current hour.
	tracker.recordEvictions(50, time.Now())

	// Snapshot — the peak should be 1.
	snap := tracker.snapshot(tmp, 1024)
	if snap == nil {
		t.Fatal("snapshot returned nil")
	}
	if snap.PeakEvictions24h != 1 {
		t.Errorf("expected PeakEvictions24h=1 (one run regardless of count), got %d", snap.PeakEvictions24h)
	}
	if snap.PeakEvictions7d != 1 {
		t.Errorf("expected PeakEvictions7d=1 (one run regardless of count), got %d", snap.PeakEvictions7d)
	}
	if snap.PeakEvictions28d != 1 {
		t.Errorf("expected PeakEvictions28d=1 (one run regardless of count), got %d", snap.PeakEvictions28d)
	}
}

// TestCacheStats_MultipleEvictionsSameHour verifies that
// multiple recordEvictions calls in the same hour merge
// into one bucket (run counts summed).
func TestCacheStats_MultipleEvictionsSameHour(t *testing.T) {
	tmp := t.TempDir()
	tracker := newCacheStatsTracker(1024)
	now := time.Now()
	// Three calls in the same hour = 3 runs total.
	tracker.recordEvictions(10, now)
	tracker.recordEvictions(20, now)
	tracker.recordEvictions(30, now)
	snap := tracker.snapshot(tmp, 1024)
	// 1 + 1 + 1 = 3 runs merged into one hour bucket
	if snap.PeakEvictions24h != 3 {
		t.Errorf("expected PeakEvictions24h=3 (one run per call, merged same-hour), got %d", snap.PeakEvictions24h)
	}
	// events slice should have exactly 1 entry
	tracker.mu.Lock()
	n := len(tracker.events)
	tracker.mu.Unlock()
	if n != 1 {
		t.Errorf("expected 1 merged event, got %d", n)
	}
}

// TestCacheStats_DifferentHours verifies that evictions in
// different hours produce different buckets.
func TestCacheStats_DifferentHours(t *testing.T) {
	tmp := t.TempDir()
	tracker := newCacheStatsTracker(1024)
	now := time.Now()
	// One run an hour ago, one run now. Each = 1 run.
	tracker.recordEvictions(5, now.Add(-time.Hour))
	tracker.recordEvictions(3, now)
	snap := tracker.snapshot(tmp, 1024)
	// The peak in any 1-hour bucket is 1 (not 1+1=2).
	if snap.PeakEvictions24h != 1 {
		t.Errorf("expected PeakEvictions24h=1 (one run per hour, max of buckets), got %d", snap.PeakEvictions24h)
	}
	// events slice should have exactly 2 entries
	tracker.mu.Lock()
	n := len(tracker.events)
	tracker.mu.Unlock()
	if n != 2 {
		t.Errorf("expected 2 events, got %d", n)
	}
}

// TestCacheStats_PruningOlderThan28Days verifies events
// older than 28 days are dropped on snapshot.
func TestCacheStats_PruningOlderThan28Days(t *testing.T) {
	tmp := t.TempDir()
	tracker := newCacheStatsTracker(1024)
	now := time.Now()
	// One run 29 days ago — should be pruned
	tracker.recordEvictions(100, now.Add(-29*24*time.Hour))
	// One run now
	tracker.recordEvictions(3, now)
	snap := tracker.snapshot(tmp, 1024)
	// After pruning, the 29-day-old events should be gone,
	// so the peak should be 1 (from the current hour).
	if snap.PeakEvictions28d != 1 {
		t.Errorf("expected PeakEvictions28d=1 (one run after pruning), got %d", snap.PeakEvictions28d)
	}
	// Events slice should have just 1 entry (the 3 from now)
	tracker.mu.Lock()
	n := len(tracker.events)
	tracker.mu.Unlock()
	if n != 1 {
		t.Errorf("expected 1 event after pruning, got %d", n)
	}
}

// TestCacheStats_WindowsCutoffs verifies the 24h / 7d / 28d
// windows use the right cutoffs.
func TestCacheStats_WindowsCutoffs(t *testing.T) {
	tmp := t.TempDir()
	tracker := newCacheStatsTracker(1024)
	now := time.Now()
	// One run 2 hours ago (within 24h, within 7d, within 28d)
	tracker.recordEvictions(12, now.Add(-2*time.Hour))
	// One run 5 days ago (NOT in 24h, within 7d, within 28d)
	tracker.recordEvictions(7, now.Add(-5*24*time.Hour))
	// One run 20 days ago (NOT in 24h, NOT in 7d, within 28d)
	tracker.recordEvictions(2, now.Add(-20*24*time.Hour))
	// One run 29 days ago (NOT in any window — pruned)
	tracker.recordEvictions(50, now.Add(-29*24*time.Hour))
	snap := tracker.snapshot(tmp, 1024)
	// All four windows should report peak = 1 (each
	// bucket has exactly one run; the run from 2h ago is
	// the only one in 24h and the largest in all windows).
	if snap.PeakEvictions24h != 1 {
		t.Errorf("expected PeakEvictions24h=1, got %d", snap.PeakEvictions24h)
	}
	if snap.PeakEvictions7d != 1 {
		t.Errorf("expected PeakEvictions7d=1, got %d", snap.PeakEvictions7d)
	}
	if snap.PeakEvictions28d != 1 {
		t.Errorf("expected PeakEvictions28d=1, got %d", snap.PeakEvictions28d)
	}
}

// TestCacheStats_ClampAt255 verifies that eviction counts
// above 255 are clamped to 255 in the peak calculation.
// Per user request 2026-07-04: recordEvictions increments
// by 1 per call regardless of the count parameter, so the
// only way to reach the 255 cap is to call recordEvictions
// 255+ times in the same hour bucket. The test below
// simulates that by calling the function 300 times in the
// current hour.
func TestCacheStats_ClampAt255(t *testing.T) {
	tmp := t.TempDir()
	tracker := newCacheStatsTracker(1024)
	now := time.Now()
	// 300 calls in the current hour = 300 runs in one
	// hour bucket = clamped to 255.
	for i := 0; i < 300; i++ {
		tracker.recordEvictions(100, now)
	}
	snap := tracker.snapshot(tmp, 1024)
	if snap.PeakEvictions24h != 255 {
		t.Errorf("expected PeakEvictions24h=255 (clamped), got %d", snap.PeakEvictions24h)
	}
}

// TestCacheStats_NilSafety verifies that nil tracker doesn't
// crash and returns nil from load.
func TestCacheStats_NilSafety(t *testing.T) {
	var tracker *cacheStatsTracker
	if stats := tracker.load(); stats != nil {
		t.Error("expected nil from nil tracker")
	}
	// recordEvictions and snapshot should be no-ops on nil
	tracker.recordEvictions(5, time.Now()) // should not crash
	if snap := tracker.snapshot("/tmp", 1024); snap != nil {
		t.Error("expected nil from nil tracker snapshot")
	}
}

// TestCacheStats_GatherSizeAndCount verifies the snapshot
// function correctly walks the directory and computes
// size + file count.
func TestCacheStats_GatherSizeAndCount(t *testing.T) {
	tmp := t.TempDir()
	// Create 3 files of 100 bytes each = 300 bytes total
	for i := 0; i < 3; i++ {
		path := filepath.Join(tmp, fmt.Sprintf("file%d.webp", i))
		if err := os.WriteFile(path, make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tracker := newCacheStatsTracker(1024)
	snap := tracker.snapshot(tmp, 1024)
	if snap.FileCount != 3 {
		t.Errorf("expected FileCount=3, got %d", snap.FileCount)
	}
	if snap.SizeBytes != 300 {
		t.Errorf("expected SizeBytes=300, got %d", snap.SizeBytes)
	}
	// CacheUsageFractionHex255 = 300 / (1024*1024*1024) * 255 ≈ 0
	if pct := snap.CacheUsageFractionHex255(); pct != 0 {
		t.Errorf("expected CacheUsageFractionHex255=0 (300 bytes < 1 GB), got %d", pct)
	}
}

// TestCacheStats_CacheUsageFractionHex255Math verifies the
// percent calculation with various inputs.
func TestCacheStats_CacheUsageFractionHex255Math(t *testing.T) {
	tests := []struct {
		name         string
		sizeBytes    int64
		capBytes     int64
		wantHex255   int
	}{
		{"empty cache", 0, 1024 * 1024 * 1024, 0},
		// half full: int(0.5 * 255) = 127 (integer division
		// truncates 127.5 to 127)
		{"half full", 512 * 1024 * 1024, 1024 * 1024 * 1024, 127},
		// full: int(1.0 * 255) = 255 (== 0xFF, "100%" maps
		// to the full byte range, matching YY/ZZ/AA)
		{"full", 1024 * 1024 * 1024, 1024 * 1024 * 1024, 255},
		// over cap: int(2.0 * 255) = 510, clamped to 255
		{"over cap", 2 * 1024 * 1024 * 1024, 1024 * 1024 * 1024, 255},
		{"tiny", 1024, 1024 * 1024 * 1024, 0}, // rounds to 0
		{"unbounded", 9999, 0, -1},              // unbounded
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &cacheStats{SizeBytes: tc.sizeBytes, CapBytes: tc.capBytes}
			if got := s.CacheUsageFractionHex255(); got != tc.wantHex255 {
				t.Errorf("CacheUsageFractionHex255() = %d, want %d (size=%d, cap=%d)", got, tc.wantHex255, tc.sizeBytes, tc.capBytes)
			}
		})
	}
}

// TestFormatCacheStatsFooter verifies the four hex strings
// produced for the footer. Per user request 2026-07-04
// (cache-status-line-updates branch): the BB (max cache
// size in hex) was added in 1.1.x.
func TestFormatCacheStatsFooter(t *testing.T) {
	tests := []struct {
		name   string
		stats  *cacheStats
		capMB  int
		wantXX string
		wantYY string
		wantZZ string
		wantAA string
		wantBB string
	}{
		{
			name:   "nil stats",
			stats:  nil,
			capMB:  1024,
			wantXX: "00", wantYY: "00", wantZZ: "00", wantAA: "00", wantBB: "00",
		},
		{
			name:   "bounded, empty",
			stats:  &cacheStats{CapBytes: 1024 * 1024 * 1024},
			capMB:  1024,
			wantXX: "00", wantYY: "00", wantZZ: "00", wantAA: "00", wantBB: "10",
		},
		{
			// half full: int(0.5 * 255) = 127 = 0x7F
			// (was "32" under the old 0-100 scale; renamed
			// the function and switched to the 0-255
			// scale so 100% maps to 0xFF like the
			// peak-eviction fields)
			name:   "bounded, half full, peaks",
			stats:  &cacheStats{SizeBytes: 512 * 1024 * 1024, CapBytes: 1024 * 1024 * 1024, PeakEvictions24h: 12, PeakEvictions7d: 30, PeakEvictions28d: 100},
			capMB:  1024,
			wantXX: "7F", wantYY: "0C", wantZZ: "1E", wantAA: "64", wantBB: "10",
		},
		{
			name:   "unbounded",
			stats:  &cacheStats{SizeBytes: 999, CapBytes: 0},
			capMB:  0,
			wantXX: "∞", wantYY: "00", wantZZ: "00", wantAA: "00", wantBB: "00",
		},
		{
			name:   "peaks clamped to 255",
			stats:  &cacheStats{CapBytes: 1024 * 1024 * 1024, PeakEvictions24h: 1000, PeakEvictions7d: 256, PeakEvictions28d: 99999},
			capMB:  1024,
			wantXX: "00", wantYY: "FF", wantZZ: "FF", wantAA: "FF", wantBB: "10",
		},
		{
			// Per user request 2026-07-04: BB shows the
			// max cache size in hex, scaled as
			// cap_in_MB / 64 so the 0-16 GB range fits
			// in 2 hex digits. 2 GB cap = 2048/64 = 32
			// = 0x20. 4 GB = 4096/64 = 64 = 0x40.
			// Need to set both SizeBytes (so XX is 0% = 00)
			// and CapBytes (to match the capMB so XX's
			// CacheUsageFractionHex255 returns 0% instead
			// of "unbounded").
			name:   "2 GB cap",
			stats:  &cacheStats{CapBytes: 2 * 1024 * 1024 * 1024},
			capMB:  2048,
			wantXX: "00", wantYY: "00", wantZZ: "00", wantAA: "00", wantBB: "20",
		},
		{
			// 16384 / 64 = 256, clamped to 255 = 0xFF.
			// CapBytes set to 16 GB so XX is 00 not ∞.
			name:   "16 GB cap (clamped to FF)",
			stats:  &cacheStats{CapBytes: 16 * 1024 * 1024 * 1024},
			capMB:  16384,
			wantXX: "00", wantYY: "00", wantZZ: "00", wantAA: "00", wantBB: "FF",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			xx, yy, zz, aa, bb := formatCacheStatsFooter(tc.stats, tc.capMB)
			if xx != tc.wantXX {
				t.Errorf("XX = %q, want %q", xx, tc.wantXX)
			}
			if yy != tc.wantYY {
				t.Errorf("YY = %q, want %q", yy, tc.wantYY)
			}
			if zz != tc.wantZZ {
				t.Errorf("ZZ = %q, want %q", zz, tc.wantZZ)
			}
			if aa != tc.wantAA {
				t.Errorf("AA = %q, want %q", aa, tc.wantAA)
			}
			if bb != tc.wantBB {
				t.Errorf("BB = %q, want %q", bb, tc.wantBB)
			}
		})
	}
}

// TestRenderPage_FooterShowsCacheStats verifies the rendered
// HTML includes the cache stats footer.
func TestRenderPage_FooterShowsCacheStats(t *testing.T) {
	files := []FileInfo{{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage}}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, false, 0, []string{"30", "60", "120", "all"}, files, nil, defaultImageExts, defaultVideoExts, defaultVideoExts, "", "", "substring", "en", nil, "32", "0C", "1E", "64", "10")
	if err != nil {
		t.Fatal(err)
	}
	// Verify the footer div is present with the right values
	if !strings.Contains(html, "32 // 0C // 1E // 64 // 10") {
		t.Error("expected cache stats line '32 // 0C // 1E // 64 // 10' in footer")
	}
	if !strings.Contains(html, "site-footer-cache-stats") {
		t.Error("expected site-footer-cache-stats class")
	}
	if !strings.Contains(html, "synapticloop // media gallery") {
		t.Error("expected the 'proudly served by' line above the cache stats")
	}
}

// TestRenderPage_FooterShowsInfinityWhenUnbounded verifies
// the XX is rendered as ∞ when CapBytes is 0.
func TestRenderPage_FooterShowsInfinityWhenUnbounded(t *testing.T) {
	files := []FileInfo{{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage}}
	// Pass the pre-formatted strings — XX is ∞, others are 00
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, false, 0, []string{"30", "60", "120", "all"}, files, nil, defaultImageExts, defaultVideoExts, defaultVideoExts, "", "", "substring", "en", nil, "∞", "00", "00", "00", "00")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "∞ // 00 // 00 // 00 // 00") {
		t.Error("expected infinity symbol in footer for unbounded cache")
	}
}

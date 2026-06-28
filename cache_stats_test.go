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
	if stats.CacheUsagePercent() != 0 {
		t.Errorf("expected CacheUsagePercent=0, got %d", stats.CacheUsagePercent())
	}
	if stats.PeakEvictions24h != 0 || stats.PeakEvictions7d != 0 || stats.PeakEvictions28d != 0 {
		t.Errorf("expected all peaks=0, got 24h=%d 7d=%d 28d=%d",
			stats.PeakEvictions24h, stats.PeakEvictions7d, stats.PeakEvictions28d)
	}
}

// TestCacheStats_UnboundedCap verifies that with CapBytes=0
// (unbounded), CacheUsagePercent returns -1.
func TestCacheStats_UnboundedCap(t *testing.T) {
	tracker := newCacheStatsTracker(0)
	stats := tracker.load()
	if stats.CapBytes != 0 {
		t.Errorf("expected CapBytes=0, got %d", stats.CapBytes)
	}
	if stats.CacheUsagePercent() != -1 {
		t.Errorf("expected CacheUsagePercent=-1 for unbounded, got %d", stats.CacheUsagePercent())
	}
}

// TestCacheStats_RecordEvictions verifies that recordEvictions
// accumulates and the peaks reflect the recorded counts.
func TestCacheStats_RecordEvictions(t *testing.T) {
	tmp := t.TempDir()
	tracker := newCacheStatsTracker(1024)

	// Record 5 evictions in the current hour
	tracker.recordEvictions(5, time.Now())

	// Snapshot — size + count are still 0 (no files), but
	// the peak should be 5.
	snap := tracker.snapshot(tmp, 1024)
	if snap == nil {
		t.Fatal("snapshot returned nil")
	}
	if snap.PeakEvictions24h != 5 {
		t.Errorf("expected PeakEvictions24h=5, got %d", snap.PeakEvictions24h)
	}
	if snap.PeakEvictions7d != 5 {
		t.Errorf("expected PeakEvictions7d=5, got %d", snap.PeakEvictions7d)
	}
	if snap.PeakEvictions28d != 5 {
		t.Errorf("expected PeakEvictions28d=5, got %d", snap.PeakEvictions28d)
	}
}

// TestCacheStats_MultipleEvictionsSameHour verifies that
// multiple recordEvictions calls in the same hour merge
// into one bucket.
func TestCacheStats_MultipleEvictionsSameHour(t *testing.T) {
	tmp := t.TempDir()
	tracker := newCacheStatsTracker(1024)
	now := time.Now()
	// Three calls in the same hour
	tracker.recordEvictions(3, now)
	tracker.recordEvictions(2, now)
	tracker.recordEvictions(4, now)
	snap := tracker.snapshot(tmp, 1024)
	// 3+2+4 = 9 evictions in one hour
	if snap.PeakEvictions24h != 9 {
		t.Errorf("expected PeakEvictions24h=9 (merged same-hour), got %d", snap.PeakEvictions24h)
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
	// 5 evictions an hour ago, 3 evictions now
	tracker.recordEvictions(5, now.Add(-time.Hour))
	tracker.recordEvictions(3, now)
	snap := tracker.snapshot(tmp, 1024)
	// The peak in any 1-hour bucket is 5 (not 3+5=8)
	if snap.PeakEvictions24h != 5 {
		t.Errorf("expected PeakEvictions24h=5 (max of buckets), got %d", snap.PeakEvictions24h)
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
	// 100 evictions 29 days ago — should be pruned
	tracker.recordEvictions(100, now.Add(-29*24*time.Hour))
	// 3 evictions now
	tracker.recordEvictions(3, now)
	snap := tracker.snapshot(tmp, 1024)
	// After pruning, the 29-day-old events should be gone,
	// so the peak should be 3 (from the current hour).
	if snap.PeakEvictions28d != 3 {
		t.Errorf("expected PeakEvictions28d=3 (after pruning), got %d", snap.PeakEvictions28d)
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
	// 12 evictions 2 hours ago (within 24h, within 7d, within 28d)
	tracker.recordEvictions(12, now.Add(-2*time.Hour))
	// 7 evictions 5 days ago (NOT in 24h, within 7d, within 28d)
	tracker.recordEvictions(7, now.Add(-5*24*time.Hour))
	// 2 evictions 20 days ago (NOT in 24h, NOT in 7d, within 28d)
	tracker.recordEvictions(2, now.Add(-20*24*time.Hour))
	// 50 evictions 29 days ago (NOT in any window — pruned)
	tracker.recordEvictions(50, now.Add(-29*24*time.Hour))
	snap := tracker.snapshot(tmp, 1024)
	// Peak 24h: max in any 1h bucket within last 24h = 12
	if snap.PeakEvictions24h != 12 {
		t.Errorf("expected PeakEvictions24h=12, got %d", snap.PeakEvictions24h)
	}
	// Peak 7d: max within last 7d = 12 (still 12, since 7 is less)
	if snap.PeakEvictions7d != 12 {
		t.Errorf("expected PeakEvictions7d=12, got %d", snap.PeakEvictions7d)
	}
	// Peak 28d: max within last 28d = 12 (the 50 was pruned)
	if snap.PeakEvictions28d != 12 {
		t.Errorf("expected PeakEvictions28d=12, got %d", snap.PeakEvictions28d)
	}
}

// TestCacheStats_ClampAt255 verifies that eviction counts
// above 255 are clamped to 255 in the peak calculation.
func TestCacheStats_ClampAt255(t *testing.T) {
	tmp := t.TempDir()
	tracker := newCacheStatsTracker(1024)
	now := time.Now()
	tracker.recordEvictions(1000, now) // clamped to 255
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
	// CacheUsagePercent = 300 / (1024*1024*1024) * 100 ≈ 0
	if pct := snap.CacheUsagePercent(); pct != 0 {
		t.Errorf("expected CacheUsagePercent=0 (300 bytes < 1 GB), got %d", pct)
	}
}

// TestCacheStats_CacheUsagePercentMath verifies the percent
// calculation with various inputs.
func TestCacheStats_CacheUsagePercentMath(t *testing.T) {
	tests := []struct {
		name        string
		sizeBytes   int64
		capBytes    int64
		wantPercent int
	}{
		{"empty cache", 0, 1024 * 1024 * 1024, 0},
		{"half full", 512 * 1024 * 1024, 1024 * 1024 * 1024, 50},
		{"full", 1024 * 1024 * 1024, 1024 * 1024 * 1024, 100},
		{"over cap", 2 * 1024 * 1024 * 1024, 1024 * 1024 * 1024, 100}, // clamped
		{"tiny", 1024, 1024 * 1024 * 1024, 0},                         // rounds to 0
		{"unbounded", 9999, 0, -1},                                    // unbounded
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &cacheStats{SizeBytes: tc.sizeBytes, CapBytes: tc.capBytes}
			if got := s.CacheUsagePercent(); got != tc.wantPercent {
				t.Errorf("CacheUsagePercent() = %d, want %d", got, tc.wantPercent)
			}
		})
	}
}

// TestFormatCacheStatsFooter verifies the four hex strings
// produced for the footer.
func TestFormatCacheStatsFooter(t *testing.T) {
	tests := []struct {
		name   string
		stats  *cacheStats
		wantXX string
		wantYY string
		wantZZ string
		wantAA string
	}{
		{
			name:   "nil stats",
			stats:  nil,
			wantXX: "00", wantYY: "00", wantZZ: "00", wantAA: "00",
		},
		{
			name:   "bounded, empty",
			stats:  &cacheStats{CapBytes: 1024 * 1024 * 1024},
			wantXX: "00", wantYY: "00", wantZZ: "00", wantAA: "00",
		},
		{
			name:   "bounded, half full, peaks",
			stats:  &cacheStats{SizeBytes: 512 * 1024 * 1024, CapBytes: 1024 * 1024 * 1024, PeakEvictions24h: 12, PeakEvictions7d: 30, PeakEvictions28d: 100},
			wantXX: "32", wantYY: "0C", wantZZ: "1E", wantAA: "64",
		},
		{
			name:   "unbounded",
			stats:  &cacheStats{SizeBytes: 999, CapBytes: 0},
			wantXX: "\u221e", wantYY: "00", wantZZ: "00", wantAA: "00",
		},
		{
			name:   "peaks clamped to 255",
			stats:  &cacheStats{CapBytes: 1024 * 1024 * 1024, PeakEvictions24h: 1000, PeakEvictions7d: 256, PeakEvictions28d: 99999},
			wantXX: "00", wantYY: "FF", wantZZ: "FF", wantAA: "FF",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			xx, yy, zz, aa := formatCacheStatsFooter(tc.stats)
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
		})
	}
}

// TestRenderPage_FooterShowsCacheStats verifies the rendered
// HTML includes the cache stats footer.
func TestRenderPage_FooterShowsCacheStats(t *testing.T) {
	files := []FileInfo{{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage}}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0,
		[]string{"30", "60", "120", "all"}, files, nil, defaultImageExts, defaultVideoExts, "", "", "substring", "32", "0C", "1E", "64")
	if err != nil {
		t.Fatal(err)
	}
	// Verify the footer div is present with the right values
	if !strings.Contains(html, "32 // 0C // 1E // 64") {
		t.Error("expected cache stats line '32 // 0C // 1E // 64' in footer")
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0,
		[]string{"30", "60", "120", "all"}, files, nil, defaultImageExts, defaultVideoExts, "", "", "substring", "\u221e", "00", "00", "00")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "\u221e // 00 // 00 // 00") {
		t.Error("expected infinity symbol in footer for unbounded cache")
	}
}

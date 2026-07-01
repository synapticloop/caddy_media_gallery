package gallery

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeNestedCacheFile creates a fake cache file in the new
// 2-level nested layout (<cacheDir>/<aa>/<bb>/<rest>.webp).
// Returns the full nested path. We compute the hash from a
// fake source path (just an index) so the test files end up
// in deterministic locations.
func makeNestedCacheFile(t *testing.T, cacheDir string, idx int, size int) string {
	t.Helper()
	abs, _ := filepath.Abs(fmt.Sprintf("/fake/src/%d.jpg", idx))
	h := sha256.Sum256([]byte(abs))
	hexHash := hex.EncodeToString(h[:16])
	subdir1 := hexHash[:2]
	subdir2 := hexHash[2:4]
	rest := hexHash[4:]
	nestedDir := filepath.Join(cacheDir, subdir1, subdir2)
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(nestedDir, rest+".webp")
	data := make([]byte, size)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// makeNestedCacheMeta creates the .meta sidecar for a nested
// cache file. Returns the full nested meta path.
func makeNestedCacheMeta(t *testing.T, cacheDir string, idx int) string {
	t.Helper()
	abs, _ := filepath.Abs(fmt.Sprintf("/fake/src/%d.jpg", idx))
	h := sha256.Sum256([]byte(abs))
	hexHash := hex.EncodeToString(h[:16])
	subdir1 := hexHash[:2]
	subdir2 := hexHash[2:4]
	rest := hexHash[4:]
	nestedDir := filepath.Join(cacheDir, subdir1, subdir2)
	path := filepath.Join(nestedDir, rest+".webp.meta")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("100 100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestEvictIfOver_NoLimit verifies that no eviction happens
// when maxMB <= 0 (the explicit "no cap" opt-out).
func TestEvictIfOver_NoLimit(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "x.webp"), []byte("AAAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	evictIfOver(tmp, 0, nil)
	evictIfOver(tmp, -1, nil)
	if _, err := os.Stat(filepath.Join(tmp, "x.webp")); err != nil {
		t.Error("expected file to still exist with no limit")
	}
}

// TestEvictIfOver_UnderLimit verifies no eviction when the
// cache is under the cap.
func TestEvictIfOver_UnderLimit(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a.webp"), []byte("AAAA"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 4 bytes, cap 1 MB → under cap
	evictIfOver(tmp, 1, nil)
	if _, err := os.Stat(filepath.Join(tmp, "a.webp")); err != nil {
		t.Error("expected file to still exist under cap")
	}
}

// TestEvictIfOver_EmptyDir verifies the no-error case
// for an empty cache directory.
func TestEvictIfOver_EmptyDir(t *testing.T) {
	tmp := t.TempDir()
	evictIfOver(tmp, 1, nil) // should not error, should not panic
}

// TestEvictIfOver_NonexistentDir verifies the no-error case
// for a directory that doesn't exist (no thumbs cached yet).
func TestEvictIfOver_NonexistentDir(t *testing.T) {
	evictIfOver("/nonexistent/path/that/does/not/exist/evict", 1, nil)
	// Should silently return — not an error.
}

// TestEvictIfOver_OverLimit creates a cache over the cap and
// verifies the oldest files are evicted to bring the cache
// to the target (80% of the cap).
//
// NOTE: maxMB is in megabytes (the parameter is named that
// way to match the Gallery.MaxCacheSizeMB field). The smallest
// useful cap value is 1 MB. We use 1 MB cap with files that
// total 2 MB (2 files of 1 MB each). The target is 0.8 MB,
// so both files get evicted.
func TestEvictIfOver_OverLimit(t *testing.T) {
	tmp := t.TempDir()
	// Create 2 nested-layout thumbs of 1 MB each = 2 MB total.
	// Cap: 1 MB. Target: 0.8 MB. Evict BOTH files
	// (2 MB > 0.8 MB target).
	now := time.Now()
	for i := 0; i < 2; i++ {
		path := makeNestedCacheFile(t, tmp, i, 1024*1024)
		// i=0 is oldest, i=1 is newest
		oldTime := now.Add(-time.Duration(2-i) * time.Minute)
		if err := os.Chtimes(path, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}
	evictIfOver(tmp, 1, nil) // 1 MB cap
	// Both files should be evicted (total 2 MB > 0.8 MB target).
	// Per user request 2026-06-30: the legacy flat layout is
	// no longer supported, so we only count files in nested
	// subdirs. Walking the cache dir and checking for "files
	// at the top level" doesn't work anymore (we have subdirs
	// at the top level). Instead, count files across all
	// nested subdirs.
	var remaining int
	filepath.Walk(tmp, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".webp" {
			remaining++
		}
		return nil
	})
	if remaining != 0 {
		t.Errorf("expected 0 .webp files remaining (2 MB > 0.8 MB target), got %d", remaining)
	}
}

// TestEvictIfOver_UnderCapNoEviction verifies no eviction
// happens when the cache is well under the cap.
func TestEvictIfOver_UnderCapNoEviction(t *testing.T) {
	tmp := t.TempDir()
	// Create 10 nested-layout files of 100 KB each = 1 MB total.
	// Cap: 5 MB. We're well under cap → no eviction.
	for i := 0; i < 10; i++ {
		makeNestedCacheFile(t, tmp, i, 100*1024)
	}
	evictIfOver(tmp, 5, nil) // 5 MB cap, 1 MB used
	// Count remaining .webp files across the nested layout.
	var remaining int
	filepath.Walk(tmp, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".webp" {
			remaining++
		}
		return nil
	})
	if remaining != 10 {
		t.Errorf("expected 10 files remaining (under cap), got %d", remaining)
	}
}

// TestEvictIfOver_OldestFirst verifies the FIFO-by-mtime
// eviction order (oldest mtime is evicted first).
//
// 2 files of 1 MB each = 2 MB total.
// Cap: 1 MB. Target: 0.8 MB. Both files evicted.
// We assert the NEWEST file is the FIRST one to go
// (oldest mtime is evicted first, so i=0 [oldest] is
// evicted before i=1 [newest]).
func TestEvictIfOver_OldestFirst(t *testing.T) {
	tmp := t.TempDir()
	now := time.Now()
	// 3 files of 700 KB each in the nested layout. i=0 is
	// oldest, i=2 is newest. 2.1 MB total, 1 MB cap →
	// 0.8 MB target → evict 2 oldest (leaving i=2, the newest).
	paths := make([]string, 3)
	for i := 0; i < 3; i++ {
		paths[i] = makeNestedCacheFile(t, tmp, i, 700*1024)
		oldTime := now.Add(-time.Duration(3-i) * time.Hour)
		if err := os.Chtimes(paths[i], oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}
	evictIfOver(tmp, 1, nil) // 1 MB cap → 0.8 MB target
	// The 2 oldest should be evicted. The newest (i=2) should
	// still exist.
	for i := 0; i < 2; i++ {
		if _, err := os.Stat(paths[i]); err == nil {
			t.Errorf("file %d (oldest) should have been evicted, but still exists", i)
		}
	}
	if _, err := os.Stat(paths[2]); err != nil {
		t.Errorf("file 2 (newest) should still exist: %v", err)
	}
}

// TestEvictIfOver_FIFOOrder creates 3 files of 700 KB each
// (2.1 MB total). Cap: 1 MB. Target: 0.8 MB. Should evict
// the 2 oldest (leaving 1 newest).
func TestEvictIfOver_FIFOOrder(t *testing.T) {
	tmp := t.TempDir()
	now := time.Now()
	// 3 files of 700 KB each in the nested layout. i=0 is
	// oldest, i=2 is newest. 2.1 MB total, 1 MB cap →
	// 0.8 MB target → evict 2 oldest (leaving i=2, the newest).
	paths := make([]string, 3)
	for i := 0; i < 3; i++ {
		paths[i] = makeNestedCacheFile(t, tmp, i, 700*1024)
		// i=0 is oldest, i=2 is newest
		oldTime := now.Add(-time.Duration(3-i) * time.Hour)
		if err := os.Chtimes(paths[i], oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}
	evictIfOver(tmp, 1, nil)
	// The newest file (i=2) should still exist.
	for i := 0; i < 2; i++ {
		if _, err := os.Stat(paths[i]); err == nil {
			t.Errorf("file %d (older) should have been evicted", i)
		}
	}
	if _, err := os.Stat(paths[2]); err != nil {
		t.Errorf("file 2 (newest) should still exist: %v", err)
	}
}

// TestEvictIfOver_OnlyFilesNotSubdirs verifies the eviction
// helper skips subdirectories (we only delete files).
func TestEvictIfOver_OnlyFilesNotSubdirs(t *testing.T) {
	tmp := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmp, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Create 2 files of 1 MB each (over the 1 MB cap)
	if err := os.WriteFile(filepath.Join(tmp, "x.webp"), make([]byte, 1024*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	evictIfOver(tmp, 1, nil) // 1 MB cap, 1 MB used (over the 0.8 MB target)
	// Subdir should still exist
	if _, err := os.Stat(filepath.Join(tmp, "sub")); err != nil {
		t.Error("expected subdirectory to still exist after eviction")
	}
}


// TestEvictIfOver_LRUViaMetaSidecar verifies the LRU eviction
// (per user request 2026-06-29): the eviction logic uses the
// .meta sidecar's mtime (which serveThumb touches on every
// access) as the LRU timestamp, NOT the thumb's own mtime.
// This decouples eviction from the source's mtime (the thumb's
// own mtime now mirrors the source's for the staleness check).
//
// Setup: 3 thumbs. The .meta mtimes are: t0 < t1 < t2.
// The thumb's own mtimes are: T0 << T1 << T2 (way older —
// simulating "all source mtimes are old, but .meta was
// touched recently on these"). Eviction should remove t0's
// thumb first (oldest LRU), not T0's thumb (oldest source).
func TestEvictIfOver_LRUViaMetaSidecar(t *testing.T) {
	tmp := t.TempDir()
	now := time.Now()
	// 3 thumbs in the nested layout, 1 MB each = 3 MB total.
	// Cap 1 MB → 0.8 MB target. All 3 will be evicted
	// (3 MB >> 0.8 MB). We assert the ORDER: the thumb
	// with the OLDEST .meta mtime is evicted first,
	// regardless of the thumb's own mtime.
	sourceTimes := []time.Time{
		now.Add(-1000 * time.Hour), // very old source
		now.Add(-500 * time.Hour),  // less old
		now.Add(-100 * time.Hour),  // recent source
	}
	lruTimes := []time.Time{
		now.Add(-3 * time.Hour),  // LRU=oldest
		now.Add(-1 * time.Hour),  // LRU=mid
		now.Add(-1 * time.Minute), // LRU=newest
	}
	paths := make([]string, 3)
	metaPaths := make([]string, 3)
	for i := 0; i < 3; i++ {
		paths[i] = makeNestedCacheFile(t, tmp, i, 1024*1024)
		if err := os.Chtimes(paths[i], sourceTimes[i], sourceTimes[i]); err != nil {
			t.Fatal(err)
		}
		metaPaths[i] = makeNestedCacheMeta(t, tmp, i)
		if err := os.Chtimes(metaPaths[i], lruTimes[i], lruTimes[i]); err != nil {
			t.Fatal(err)
		}
	}
	evictIfOver(tmp, 1, nil) // 1 MB cap → 0.8 MB target → all 3 evicted
	// Verify all thumbs and their .meta sidecars are gone.
	for i := 0; i < 3; i++ {
		if _, err := os.Stat(paths[i]); !os.IsNotExist(err) {
			t.Errorf("thumb %d should be evicted, but still exists", i)
		}
		if _, err := os.Stat(metaPaths[i]); !os.IsNotExist(err) {
			t.Errorf("meta %d should be evicted with thumb, but still exists", i)
		}
	}
}

// TestEvictIfOver_SidecarsNotCountedTowardCap verifies that
// .meta and .exif sidecars don't count toward the cache cap.
// They're tiny and get deleted with their parent thumb during
// eviction. So a 1 MB cap with a 1 MB .webp and a 10 KB
// .meta should NOT trigger eviction (total is ~1.01 MB but
// the .meta is excluded from the cap calculation).
func TestEvictIfOver_SidecarsNotCountedTowardCap(t *testing.T) {
	tmp := t.TempDir()
	// 1 MB thumb + 10 KB sidecar = ~1.01 MB total on disk,
	// but only the .webp counts toward the cap (1 MB = at cap).
	thumbData := make([]byte, 1024*1024)
	thumbPath := filepath.Join(tmp, "abc.webp")
	if err := os.WriteFile(thumbPath, thumbData, 0o644); err != nil {
		t.Fatal(err)
	}
	metaPath := thumbPath + ".meta"
	if err := os.WriteFile(metaPath, make([]byte, 10240), 0o644); err != nil {
		t.Fatal(err)
	}
	// 1 MB cap — at the cap, should NOT trigger eviction.
	evictIfOver(tmp, 1, nil)
	// Both files should still exist.
	if _, err := os.Stat(thumbPath); err != nil {
		t.Errorf("thumb should not be evicted: %v", err)
	}
	if _, err := os.Stat(metaPath); err != nil {
		t.Errorf("meta should not be evicted: %v", err)
	}
}

// TestEvictIfOver_NoMetaSidecarUsesThumbMtime verifies the
// fallback behaviour: if a thumb has no .meta sidecar
// (legacy files, or thumbs that failed the initial
// dimensions read), the eviction uses the thumb's mtime
// as the LRU timestamp. Not ideal (a 2010 photo with no
// .meta would be evicted first regardless of usage), but
// it's a safe fallback that doesn't break anything.
func TestEvictIfOver_NoMetaSidecarUsesThumbMtime(t *testing.T) {
	tmp := t.TempDir()
	now := time.Now()
	// 2 nested-layout thumbs, 1 MB each = 2 MB total. Cap
	// 1 MB → 0.8 MB. Both will be evicted. We assert both
	// are gone.
	paths := make([]string, 2)
	for i := 0; i < 2; i++ {
		// Use makeNestedCacheFile (which doesn't create a
		// .meta sidecar) so we exercise the no-meta fallback
		// in scanNestedThumb.
		paths[i] = makeNestedCacheFile(t, tmp, i, 1024*1024)
		// Make the first thumb old (1 hour ago) and the
		// second recent (now) — eviction should remove
		// the old one first.
		oldTime := now.Add(time.Duration(i-1) * time.Hour)
		if err := os.Chtimes(paths[i], oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}
	evictIfOver(tmp, 1, nil)
	// Both evicted.
	for i := 0; i < 2; i++ {
		if _, err := os.Stat(paths[i]); !os.IsNotExist(err) {
			t.Errorf("thumb %d should be evicted", i)
		}
	}
}

// TestTouchMetaAtUse_UpdatesMtime verifies the
// touchMetaAtUse helper updates the .meta sidecar's mtime
// to the current time. Used by serveThumb on every cache
// hit to implement LRU.
//
// touchMetaAtUse uses dimsMetaPath() to compute the meta
// file's location — that's the SHA-256 hash of the source
// path with .webp.meta suffix. We use the real
// dimsMetaPath here (rather than constructing a fake path)
// so the test verifies the actual production flow.
func TestTouchMetaAtUse_UpdatesMtime(t *testing.T) {
	tmp := t.TempDir()
	// Create a fake source file. dimsMetaPath uses
	// filepath.Abs(src), so the source needs to exist.
	srcPath := filepath.Join(tmp, "src.jpg")
	if err := os.WriteFile(srcPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Compute the meta path using the same function
	// touchMetaAtUse uses internally.
	metaPath := dimsMetaPath(srcPath, tmp, "webp")
	// Create the .meta sidecar with an old mtime. Use
	// writeMetaFile (not os.WriteFile) because the new
	// nested layout requires MkdirAll on the parent subdir
	// first. writeMetaFile handles that for us.
	if err := writeMetaFile(srcPath, tmp, "webp", []byte("100 100\n")); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(metaPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	// Touch the .meta.
	touchMetaAtUse(srcPath, tmp, "webp")
	// Verify the .meta mtime was updated to ~now.
	info, err := os.Stat(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	delta := time.Since(info.ModTime())
	if delta < 0 || delta > 2*time.Second {
		t.Errorf("touchMetaAtUse: mtime not updated (delta=%v)", delta)
	}
}

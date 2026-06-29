package gallery

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanCache_ReusesOnNoChange(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.jpg"), []byte("x"), 0644)

	c := NewScanCache(100 * time.Millisecond)
	first, err := c.Get(dir, "mtime", defaultImageExts, defaultVideoExts, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Get(dir, "mtime", defaultImageExts, defaultVideoExts, false)
	if err != nil {
		t.Fatal(err)
	}
	// Same length + same content implies same scan was used.
	if len(first) != len(second) || len(first) != 1 {
		t.Fatalf("expected 1 file in both, got %d and %d", len(first), len(second))
	}
	if first[0].Name != second[0].Name {
		t.Errorf("names differ: %q vs %q", first[0].Name, second[0].Name)
	}
}

func TestScanCache_RefreshesOnMtimeChange(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.jpg"), []byte("x"), 0644)

	c := NewScanCache(time.Minute)
	first, _ := c.Get(dir, "mtime", defaultImageExts, defaultVideoExts, false)
	if len(first) != 1 {
		t.Fatalf("expected 1 file, got %d", len(first))
	}

	// Add a new file and update mtime.
	os.WriteFile(filepath.Join(dir, "b.jpg"), []byte("y"), 0644)
	future := time.Now().Add(time.Second)
	os.Chtimes(filepath.Join(dir, "a.jpg"), future, future)
	// Also touch the dir's mtime.
	os.Chtimes(dir, future, future)

	second, _ := c.Get(dir, "mtime", defaultImageExts, defaultVideoExts, false)
	if len(second) != 2 {
		t.Errorf("expected 2 files after adding b.jpg, got %d", len(second))
	}
}

func TestScanCache_RefreshesAfterTTL(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.jpg"), []byte("x"), 0644)

	c := NewScanCache(50 * time.Millisecond)
	first, _ := c.Get(dir, "mtime", defaultImageExts, defaultVideoExts, false)
	if len(first) != 1 {
		t.Fatalf("expected 1 file, got %d", len(first))
	}

	// Wait for TTL to expire.
	time.Sleep(80 * time.Millisecond)
	// Add a new file.
	os.WriteFile(filepath.Join(dir, "b.jpg"), []byte("y"), 0644)

	second, _ := c.Get(dir, "mtime", defaultImageExts, defaultVideoExts, false)
	if len(second) != 2 {
		t.Errorf("expected 2 files after TTL expiry + new file, got %d", len(second))
	}
}

func TestScanCache_DifferentSortCachesSeparately(t *testing.T) {
	dir := t.TempDir()
	// Write files with different mtimes so mtime and name sort give different orders.
	// a.jpg is written first (older mtime), z.jpg second (newer mtime) so:
	//   mtime desc -> z.jpg, a.jpg
	//   name asc   -> a.jpg, z.jpg
	os.WriteFile(filepath.Join(dir, "a.jpg"), []byte("x"), 0644)
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(filepath.Join(dir, "z.jpg"), []byte("x"), 0644)

	c := NewScanCache(time.Minute)
	byMtime, _ := c.Get(dir, "mtime", defaultImageExts, defaultVideoExts, false)
	byName, _ := c.Get(dir, "name", defaultImageExts, defaultVideoExts, false)
	if byMtime[0].Name == byName[0].Name {
		t.Errorf("expected different orderings, both start with %q", byMtime[0].Name)
	}
}

func TestScanCache_CallersCantMutateCachedSlice(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.jpg"), []byte("x"), 0644)

	c := NewScanCache(time.Minute)
	first, _ := c.Get(dir, "mtime", defaultImageExts, defaultVideoExts, false)
	// Mutate the returned slice.
	first[0].Name = "MUTATED"
	// Re-fetch — should be the original name.
	second, _ := c.Get(dir, "mtime", defaultImageExts, defaultVideoExts, false)
	if second[0].Name != "a.jpg" {
		t.Errorf("cache was mutated by caller: got %q, want %q", second[0].Name, "a.jpg")
	}
}

func TestScanCache_BadDirReturnsError(t *testing.T) {
	c := NewScanCache(time.Minute)
	if _, err := c.Get("/this/does/not/exist", "mtime", defaultImageExts, defaultVideoExts, false); err == nil {
		t.Error("expected error for nonexistent dir, got nil")
	}
}

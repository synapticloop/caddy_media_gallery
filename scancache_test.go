package gallery

import (
	"os"
	"testing"
	"time"
)

// TestLRUListBasics verifies the lruList data structure:
// push, move-to-front, remove, back.
func TestLRUListBasics(t *testing.T) {
	l := newLRUList()
	// Empty list
	if got := l.backKey(); got != "" {
		t.Errorf("empty list: backKey = %q, want empty string", got)
	}
	if got := l.len(); got != 0 {
		t.Errorf("empty list: len = %d, want 0", got)
	}
	// Push 3 keys
	l.pushFront("a")
	l.pushFront("b")
	l.pushFront("c")
	// Order should be c, b, a (front to back)
	if got := l.backKey(); got != "a" {
		t.Errorf("after 3 pushes: backKey = %q, want %q", got, "a")
	}
	if got := l.len(); got != 3 {
		t.Errorf("after 3 pushes: len = %d, want 3", got)
	}
	// Touch "a" — should move to front
	l.pushFront("a")
	if got := l.backKey(); got != "b" {
		t.Errorf("after touch a: backKey = %q, want %q", got, "b")
	}
	// Remove the front
	l.remove("a")
	if got := l.backKey(); got != "b" {
		t.Errorf("after remove a: backKey = %q, want %q", got, "b")
	}
	// Remove the back
	l.remove("b")
	if got := l.backKey(); got != "c" {
		t.Errorf("after remove b: backKey = %q, want %q", got, "c")
	}
	// Remove the last one
	l.remove("c")
	if got := l.len(); got != 0 {
		t.Errorf("after remove all: len = %d, want 0", got)
	}
}

// TestLRUListMoveToFrontAlreadyFront verifies that
// moveToFront is a no-op when the node is already at
// the front (no infinite loop).
func TestLRUListMoveToFrontAlreadyFront(t *testing.T) {
	l := newLRUList()
	l.pushFront("a")
	l.pushFront("b")
	// Now b is at front. Touch b (already at front).
	el := l.lookup["b"]
	l.moveToFront(el)
	if l.front.key != "b" {
		t.Errorf("after touch already-front: front = %q, want b", l.front.key)
	}
	if l.back.key != "a" {
		t.Errorf("after touch already-front: back = %q, want a", l.back.key)
	}
}

// TestLRUListRemoveNonExistent verifies that removing
// a non-existent key is a no-op.
func TestLRUListRemoveNonExistent(t *testing.T) {
	l := newLRUList()
	l.pushFront("a")
	l.remove("nonexistent")
	if l.len() != 1 {
		t.Errorf("after remove non-existent: len = %d, want 1", l.len())
	}
}

// TestScanCacheLRUEviction verifies that the cache
// evicts the LRU entry when the cap is reached.
func TestScanCacheLRUEviction(t *testing.T) {
	// Create 3 real directories so the cache accepts them.
	tmp := t.TempDir()
	dirs := []string{
		tmp + "/dir1",
		tmp + "/dir2",
		tmp + "/dir3",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		// Add a file so the scanner returns something
		if err := os.WriteFile(d+"/file.txt", []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	c := NewScanCacheWithCap(time.Minute, 2) // cap = 2 entries
	defaultImageExts := map[string]bool{".jpg": true}
	defaultVideoExts := map[string]bool{".mp4": true}
	defaultAudioExts := map[string]bool{".mp3": true}

	// Scan all 3 directories.
	for _, d := range dirs {
		_, err := c.Get(d, "name", defaultImageExts, defaultVideoExts, defaultAudioExts, false, false, false, tmp, "webp")
		if err != nil {
			t.Fatalf("Get(%q) failed: %v", d, err)
		}
	}
	// Cache should have at most 2 entries now (dir1 was
	// evicted when dir3 was added).
	c.mu.Lock()
	n := len(c.items)
	c.mu.Unlock()
	if n != 2 {
		t.Errorf("expected 2 entries after 3 inserts with cap 2, got %d", n)
	}
	// dir1 should be the LRU victim.
	c.mu.RLock()
	_, hasDir1 := c.items[dirs[0]]
	c.mu.RUnlock()
	if hasDir1 {
		t.Error("dir1 (the first insert, LRU) should have been evicted")
	}
	// dir2 and dir3 should still be cached.
	c.mu.RLock()
	_, hasDir2 := c.items[dirs[1]]
	_, hasDir3 := c.items[dirs[2]]
	c.mu.RUnlock()
	if !hasDir2 {
		t.Error("dir2 should still be in the cache")
	}
	if !hasDir3 {
		t.Error("dir3 should still be in the cache")
	}
}

// TestScanCacheLRUTouchRefreshes verifies that a Get
// on an existing entry refreshes the LRU recency
// (so the entry doesn't get evicted on the next insert).
func TestScanCacheLRUTouchRefreshes(t *testing.T) {
	tmp := t.TempDir()
	dirs := []string{
		tmp + "/dir1",
		tmp + "/dir2",
		tmp + "/dir3",
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(d+"/file.txt", []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := NewScanCacheWithCap(time.Minute, 2)
	defaultImageExts := map[string]bool{".jpg": true}
	defaultVideoExts := map[string]bool{".mp4": true}
	defaultAudioExts := map[string]bool{".mp3": true}

	// Scan dir1, dir2, dir3 — dir1 evicted (cap = 2).
	for _, d := range dirs {
		_, err := c.Get(d, "name", defaultImageExts, defaultVideoExts, defaultAudioExts, false, false, false, tmp, "webp")
		if err != nil {
			t.Fatal(err)
		}
	}
	// Re-scan dir1 (cache miss because evicted). dir2 evicted.
	if _, err := c.Get(dirs[0], "name", defaultImageExts, defaultVideoExts, defaultAudioExts, false, false, false, tmp, "webp"); err != nil {
		t.Fatal(err)
	}
	// Touch dir1 again (this should refresh its recency).
	if _, err := c.Get(dirs[0], "name", defaultImageExts, defaultVideoExts, defaultAudioExts, false, false, false, tmp, "webp"); err != nil {
		t.Fatal(err)
	}
	// Now scan dir3. dir2 (the LRU) should be evicted, dir1 should remain.
	if _, err := c.Get(dirs[2], "name", defaultImageExts, defaultVideoExts, defaultAudioExts, false, false, false, tmp, "webp"); err != nil {
		t.Fatal(err)
	}
	c.mu.RLock()
	_, hasDir1 := c.items[dirs[0]]
	_, hasDir2 := c.items[dirs[1]]
	_, hasDir3 := c.items[dirs[2]]
	c.mu.RUnlock()
	if !hasDir1 {
		t.Error("dir1 should still be cached (was touched)")
	}
	if hasDir2 {
		t.Error("dir2 should have been evicted (LRU)")
	}
	if !hasDir3 {
		t.Error("dir3 should be cached (just inserted)")
	}
}

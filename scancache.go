package gallery

import (
	"os"
	"sync"
	"time"
)

// ScanCache is a small in-memory cache of recent directory scans.
// Cache entries are keyed by absolute directory path; an entry is
// valid as long as the directory's mtime has not changed AND the
// entry's TTL has not expired.
//
// The cache eliminates the per-request os.ReadDir cost in directories
// that don't change often (the common case for a server with photos
// on disk). For 100+ image directories like /images/generated, this
// drops per-request work from milliseconds to microseconds.
//
// This is intentionally simple: no eviction policy beyond TTL — old
// entries are dropped when re-accessed after the TTL. For a server
// with <1000 galleries in active rotation, memory is bounded by
// (number of dirs) * (average file count) * (size of FileInfo).
type ScanCache struct {
	mu    sync.RWMutex
	ttl   time.Duration
	items map[string]scanCacheEntry
}

type scanCacheEntry struct {
	files    []FileInfo
	dirMtime time.Time
	expires  time.Time
	sort     string // Sort mode used for this entry — different sorts cache separately
}

// NewScanCache returns a cache with the given TTL. A TTL of 1 minute
// is a good default; it limits staleness if files are added/removed
// while also avoiding constant rescans for active directories.
func NewScanCache(ttl time.Duration) *ScanCache {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &ScanCache{ttl: ttl, items: make(map[string]scanCacheEntry)}
}

// Get returns the cached []FileInfo for dir, or runs a fresh scan if
// the cache is empty/expired/stale. The sortMode is part of the cache
// key — sorting by name vs mtime gives different results.
func (c *ScanCache) Get(dir, sortMode string) ([]FileInfo, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	dirMtime := info.ModTime()
	now := time.Now()

	// Fast path: read lock for cache hit.
	c.mu.RLock()
	entry, ok := c.items[dir]
	c.mu.RUnlock()
	if ok && entry.sort == sortMode && entry.dirMtime.Equal(dirMtime) && now.Before(entry.expires) {
		// Return a copy so callers can't mutate the cached slice.
		out := make([]FileInfo, len(entry.files))
		copy(out, entry.files)
		return out, nil
	}

	// Slow path: take the write lock, re-check (double-checked locking),
	// then scan and store.
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok = c.items[dir]
	if ok && entry.sort == sortMode && entry.dirMtime.Equal(dirMtime) && now.Before(entry.expires) {
		out := make([]FileInfo, len(entry.files))
		copy(out, entry.files)
		return out, nil
	}

	scanner := &Scanner{Root: dir, Sort: sortMode}
	files, err := scanner.Scan()
	if err != nil {
		return nil, err
	}
	c.items[dir] = scanCacheEntry{
		files:    files,
		dirMtime: dirMtime,
		expires:  now.Add(c.ttl),
		sort:     sortMode,
	}
	// Return a copy of the slice we just stored (so callers can't mutate cache).
	out := make([]FileInfo, len(files))
	copy(out, files)
	return out, nil
}

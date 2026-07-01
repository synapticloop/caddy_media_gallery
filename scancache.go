package gallery

import (
	"os"
	"sort"
	"strings"
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
	files      []FileInfo
	dirMtime   time.Time
	expires    time.Time
	sort       string // Sort mode used for this entry — different sorts cache separately
	extSetsKey string // Hash of (imageExts + videoExts) at scan time; if the Gallery's
	//               ext sets change, the cache is invalidated (otherwise
	//               the Gallery would re-classify files but the cached
	//               FileInfo would still have the OLD Kind).
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

// SetFiles atomically replaces the files slice for a cached entry.
// Used by the background enrichment goroutine after it finishes
// populating EXIF + dimensions on the previously-stored
// non-enriched file list.
//
// Per user report 2026-07-01: the previous pattern (mutating
// entry.files in place from the goroutine) caused a data race —
// subsequent cache hits within the TTL would return a copy of
// the slice at an arbitrary moment in the enrichment, so the
// same page could return different EXIF data on each refresh
// until the enrichment finally completed.
//
// The fix: the cache holds a non-enriched snapshot while the
// enrichment runs (callers get a copy of that snapshot). When
// the enrichment finishes, the goroutine calls SetFiles which
// atomically swaps in the enriched slice. Future cache hits see
// the enriched data; no in-progress mutation is observable.
//
// SetFiles is a no-op if the entry no longer exists (e.g. the
// cache TTL expired and the entry was dropped, or the dir mtime
// changed and the entry was replaced by a fresh scan).
func (c *ScanCache) SetFiles(dir string, files []FileInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[dir]
	if !ok {
		return
	}
	entry.files = files
	c.items[dir] = entry
}

// Get returns the cached []FileInfo for dir, or runs a fresh scan if
// the cache is empty/expired/stale. The sortMode is part of the cache
// key — sorting by name vs mtime gives different results.
//
// imageExts and videoExts are the Gallery's configured extension
// sets (used by Scanner.Classify to decide KindImage vs KindVideo vs
// KindOther). They are part of the cache key because a Gallery
// reconfigured to recognise a new extension should re-scan.
func (c *ScanCache) Get(dir, sortMode string, imageExts, videoExts map[string]bool, noExif bool, thumbCacheDir, thumbFormat string) ([]FileInfo, error) {
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
	extKey := extSetsKey(imageExts, videoExts, noExif)
	if ok && entry.sort == sortMode && entry.extSetsKey == extKey && entry.dirMtime.Equal(dirMtime) && now.Before(entry.expires) {
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
	if ok && entry.sort == sortMode && entry.extSetsKey == extKey && entry.dirMtime.Equal(dirMtime) && now.Before(entry.expires) {
		out := make([]FileInfo, len(entry.files))
		copy(out, entry.files)
		return out, nil
	}

	scanner := &Scanner{Root: dir, Sort: sortMode, ImageExts: imageExts, VideoExts: videoExts, NoExif: noExif, ThumbCacheDir: thumbCacheDir, ThumbFormat: thumbFormat}
	files, err := scanner.Scan()
	if err != nil {
		return nil, err
	}
	c.items[dir] = scanCacheEntry{
		files:      files,
		dirMtime:   dirMtime,
		expires:    now.Add(c.ttl),
		sort:       sortMode,
		extSetsKey: extKey,
	}
	// Per user report 2026-07-01: kick off the EXIF/dimensions
	// enrichment in the BACKGROUND so the visitor doesn't
	// wait for it. The first page render shows cards without
	// the EXIF pill or dimensions watermark; subsequent
	// renders (after Enrich completes + SetFiles swaps the
	// enriched slice in) show the full data. EnrichInBackground
	// mutates its own copy and calls cache.SetFiles when done
	// to atomically replace the cache entry — no data race
	// for concurrent cache readers.
	scanner.EnrichInBackground(files, c, dir)
	// Return a copy of the slice we just stored (so callers can't mutate cache).
	out := make([]FileInfo, len(files))
	copy(out, files)
	return out, nil
}

// extSetsKey returns a short string that uniquely identifies the
// pair of extension sets (imageExts + videoExts). Used as part
// of the scan cache key so a Gallery reconfigured to recognise
// new extensions invalidates its cached scans (otherwise the
// Gallery would re-classify files but the cached FileInfo entries
// would still have the OLD Kind).
//
// The key is a simple concatenation of the sorted extension
// lists — not a cryptographic hash, just a string-compare-
// equality. Two galleries with the same image+video sets get the
// same key (which is what we want: they CAN share a cache entry).
//
// Cheap to compute (one sort + one string concat per cache lookup)
// and cheap to compare (one string compare).
func extSetsKey(imageExts, videoExts map[string]bool, noExif bool) string {
	imgKeys := make([]string, 0, len(imageExts))
	for k := range imageExts {
		imgKeys = append(imgKeys, k)
	}
	sort.Strings(imgKeys)
	vidKeys := make([]string, 0, len(videoExts))
	for k := range videoExts {
		vidKeys = append(vidKeys, k)
	}
	sort.Strings(vidKeys)
	noExifStr := "0"
	if noExif {
		noExifStr = "1"
	}
	return "i:" + strings.Join(imgKeys, ",") + "|v:" + strings.Join(vidKeys, ",") + "|e:" + noExifStr
}

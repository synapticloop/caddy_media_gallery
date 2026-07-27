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
// Per user request 2026-07-04 (minor-fixes branch, fix #2): the
// cache is bounded by a maxEntries cap (default 1024). When a Set
// or slow-path Get would push the cache over the cap, the
// least-recently-accessed entry is evicted first. This protects
// against unbounded memory growth on long-running Caddy processes
// serving many directories. The cap is configurable via
// NewScanCacheWithCap for tests.
type ScanCache struct {
	mu         sync.RWMutex
	ttl        time.Duration
	items      map[string]scanCacheEntry
	// recency is a doubly-linked list tracking access order.
	// The most-recently-accessed key is at the front, the
	// least-recently-accessed at the back. Eviction removes
	// from the back. Using a list + map (rather than a heap)
	// gives O(1) access, O(1) recency update, and O(1)
	// eviction. The map keys point to list elements.
	recency    *lruList
	maxEntries int
}

// maxScanCacheEntries is the default LRU cap for the scan cache.
// 1024 entries × ~50 files per dir × ~200 bytes per FileInfo
// ≈ 10 MB worst case. Plenty for any reasonable server; the
// cap exists to bound memory in the long-tail case of
// long-running processes serving millions of directories.
const defaultScanCacheMaxEntries = 1024

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

// NewScanCache returns a cache with the given TTL and the
// default maxEntries cap. A TTL of 1 minute is a good
// default; it limits staleness if files are added/removed
// while also avoiding constant rescans for active directories.
func NewScanCache(ttl time.Duration) *ScanCache {
	if ttl <= 0 {
		ttl = time.Minute
	}
	return &ScanCache{
		ttl:        ttl,
		items:      make(map[string]scanCacheEntry),
		recency:    newLRUList(),
		maxEntries: defaultScanCacheMaxEntries,
	}
}

// NewScanCacheWithCap is like NewScanCache but allows the caller
// to override the LRU cap. Used by tests; production code should
// stick with NewScanCache.
func NewScanCacheWithCap(ttl time.Duration, maxEntries int) *ScanCache {
	c := NewScanCache(ttl)
	if maxEntries > 0 {
		c.maxEntries = maxEntries
	}
	return c
}

// touch moves dir to the front of the recency list (most
// recently accessed). Caller must hold c.mu for writing.
func (c *ScanCache) touch(dir string) {
	if el, ok := c.recency.lookup[dir]; ok {
		c.recency.moveToFront(el)
	}
}

// evictLRU removes the least-recently-accessed entry from
// items and the recency list. Caller must hold c.mu for
// writing. No-op if the cache is empty.
func (c *ScanCache) evictLRU() {
	dir := c.recency.backKey()
	if dir == "" {
		return
	}
	c.recency.remove(dir)
	delete(c.items, dir)
}

// enforceCap evicts LRU entries until len(c.items) <= c.maxEntries.
// Caller must hold c.mu for writing. Used after Set / SetFiles
// to keep the cache bounded.
func (c *ScanCache) enforceCap() {
	for len(c.items) > c.maxEntries {
		c.evictLRU()
	}
}

// lruNode is a single node in the lruList. Holds the key
// (a directory path) and the prev/next pointers.
type lruNode struct {
	key  string
	prev *lruNode
	next *lruNode
}

// lruList is a doubly-linked list of lruNode entries with
// front and back sentinels. O(1) moveToFront, remove, back.
// A map (lookup) gives O(1) key → node lookup.
type lruList struct {
	front  *lruNode
	back   *lruNode
	lookup map[string]*lruNode
}

func newLRUList() *lruList {
	return &lruList{lookup: make(map[string]*lruNode)}
}

// pushFront adds key to the front of the list. If key is
// already present, it's moved to the front (no duplicate
// entries).
func (l *lruList) pushFront(key string) {
	if el, ok := l.lookup[key]; ok {
		l.moveToFront(el)
		return
	}
	el := &lruNode{key: key}
	l.lookup[key] = el
	if l.front == nil {
		// Empty list — this is the only node.
		l.front = el
		l.back = el
		return
	}
	el.next = l.front
	l.front.prev = el
	l.front = el
}

// moveToFront moves the given node to the front of the list.
// Caller must ensure the node is still in the list.
func (l *lruList) moveToFront(el *lruNode) {
	if l.front == el {
		return // already at front
	}
	// Detach from current position
	if el.prev != nil {
		el.prev.next = el.next
	}
	if el.next != nil {
		el.next.prev = el.prev
	}
	if el == l.back {
		l.back = el.prev
	}
	// Insert at front
	el.prev = nil
	el.next = l.front
	if l.front != nil {
		l.front.prev = el
	}
	l.front = el
}

// remove removes key from the list. No-op if not present.
func (l *lruList) remove(key string) {
	el, ok := l.lookup[key]
	if !ok {
		return
	}
	if el.prev != nil {
		el.prev.next = el.next
	}
	if el.next != nil {
		el.next.prev = el.prev
	}
	if el == l.front {
		l.front = el.next
	}
	if el == l.back {
		l.back = el.prev
	}
	delete(l.lookup, key)
}

// backKey returns the key at the back of the list (the LRU
// candidate for eviction). Returns "" if the list is empty.
func (l *lruList) backKey() string {
	if l.back == nil {
		return ""
	}
	return l.back.key
}

// len returns the number of entries in the list.
func (l *lruList) len() int {
	return len(l.lookup)
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
	// Per user request 2026-07-04 (minor-fixes branch, fix
	// #2): refresh the LRU recency. SetFiles is called by
	// the background enrichment goroutine which is a
	// "touch" of the cached entry. Then enforce the cap
	// in case a long-running process has accumulated
	// more entries than the LRU allows.
	c.touch(dir)
	c.enforceCap()
}

// Get returns the cached []FileInfo for dir, or runs a fresh scan if
// the cache is empty/expired/stale. The sortMode is part of the cache
// key — sorting by name vs mtime gives different results.
//
// imageExts and videoExts are the Gallery's configured extension
// sets (used by Scanner.Classify to decide KindImage vs KindVideo vs
// KindOther). They are part of the cache key because a Gallery
// reconfigured to recognise a new extension should re-scan.
func (c *ScanCache) Get(dir, sortMode string, imageExts, videoExts, audioExts map[string]bool, noExif, noMeta, noAudioMeta bool, thumbCacheDir, thumbFormat string) ([]FileInfo, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	dirMtime := info.ModTime()
	now := time.Now()

	extKey := extSetsKey(imageExts, videoExts, audioExts, noExif, noMeta, noAudioMeta)
	// Per user request 2026-07-04 (minor-fixes branch, fix
	// #2): we now always take the write lock (not RLock)
	// because we need to update the LRU recency on every
	// cache hit. Previously we used RLock for the fast
	// path to allow concurrent readers. The new design
	// trades a tiny bit of concurrency for correct LRU
	// semantics. For the typical workload (a few
	// visitors browsing the same dir), contention is
	// minimal.
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[dir]
	if ok && entry.sort == sortMode && entry.extSetsKey == extKey && entry.dirMtime.Equal(dirMtime) && now.Before(entry.expires) {
		// Cache hit — refresh the LRU recency and
		// return a copy of the files slice.
		c.touch(dir)
		out := make([]FileInfo, len(entry.files))
		copy(out, entry.files)
		return out, nil
	}

	scanner := &Scanner{Root: dir, Sort: sortMode, ImageExts: imageExts, VideoExts: videoExts, AudioExts: audioExts, NoExif: noExif, NoMeta: noMeta, NoAudioMeta: noAudioMeta, ThumbCacheDir: thumbCacheDir, ThumbFormat: thumbFormat}
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
	// Per user request 2026-07-04 (minor-fixes branch, fix
	// #2): record the new entry in the LRU list and evict
	// LRU entries if the cache is over the cap. The slow
	// path already holds c.mu for writing, so this is
	// safe.
	c.recency.pushFront(dir)
	c.enforceCap()
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
func extSetsKey(imageExts, videoExts, audioExts map[string]bool, noExif, noMeta, noAudioMeta bool) string {
	// Per user request 2026-07-02: include noExif AND noMeta
	// in the cache key. If either flag changes, the cache
	// is invalidated (otherwise the Gallery would re-classify
	// files but the cached FileInfo would still have the OLD
	// EXIF/VideoMeta values — e.g. switching from no_meta=false
	// to no_meta=true would return cached entries with
	// VideoMeta populated, showing META pills that should be
	// hidden). Both flags affect FileInfo fields (Exif,
	// VideoMeta), so both must be in the key.
	//
	// Per user request 2026-07-04 (audio-integration branch):
	// audioExts is also part of the key (operators who set
	// audio_types shouldn't see stale KindAudio=0 entries
	// from a previous config). noAudioMeta is in the key for
	// the same reason as noExif / noMeta — toggling the audio
	// enrichment flag should invalidate cache so AudioMeta
	// reflects the new setting.
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
	audKeys := make([]string, 0, len(audioExts))
	for k := range audioExts {
		audKeys = append(audKeys, k)
	}
	sort.Strings(audKeys)
	noExifStr := "0"
	if noExif {
		noExifStr = "1"
	}
	noMetaStr := "0"
	if noMeta {
		noMetaStr = "1"
	}
	noAudioMetaStr := "0"
	if noAudioMeta {
		noAudioMetaStr = "1"
	}
	// Per user request 2026-07-02: include noExif AND
	// noMeta in the cache key. Per the previous comment
	// block, if either flag changes, the cache should be
	// invalidated (otherwise the Gallery would re-classify
	// files but the cached FileInfo would still have the OLD
	// EXIF/VideoMeta values). Both flags affect FileInfo
	// fields (Exif, VideoMeta), so both must be in the key.
	return "i:" + strings.Join(imgKeys, ",") + "|v:" + strings.Join(vidKeys, ",") + "|a:" + strings.Join(audKeys, ",") + "|e:" + noExifStr + "|m:" + noMetaStr + "|M:" + noAudioMetaStr
}

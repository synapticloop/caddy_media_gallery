// Package gallery implements a media-gallery HTTP handler for Caddy v2.
// Supports images (jpg, png, gif, webp, avif, svg) and videos (mp4, webm,
// mov, mkv, etc.) with on-demand WebP thumbnails for images and
// ffmpeg-extracted first-frame thumbnails for videos. Replaces Caddy's
// default  with a richer directory listing that
// includes a lightbox, on-the-fly image thumbnailing, and video
// preview generation.
package gallery

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

func init() {
	caddy.RegisterModule(Gallery{})
	httpcaddyfile.RegisterHandlerDirective("media_gallery", parseCaddyfile)
	httpcaddyfile.RegisterDirectiveOrder("media_gallery", httpcaddyfile.Before, "file_server")
}

func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	g := new(Gallery)
	err := g.UnmarshalCaddyfile(h.Dispenser)
	return g, err
}

// Gallery is a Caddy HTTP handler that renders a directory as a
// dark-themed image/video gallery. See the README for behaviour.
type Gallery struct {
	// Root is the on-disk directory to render. Set automatically by
	// Caddy's  directive (via Provision), or can be set in JSON
	// config.
	Root string

	// PathPrefix is the URL mount prefix for the gallery
	// (e.g. "images" if the gallery is mounted at /images/*
	// in the Caddyfile). It is used by the breadcrumb so
	// the first segment matches what the user sees in the
	// URL. Defaults to filepath.Base(Root) if empty.
	PathPrefix string
	// RootName is the operator-configurable display name for
	// the first breadcrumb segment (the gallery's root). Set
	// in the Caddyfile via . If empty,
	// the resolved rootName (set in Provision) defaults to
	// "media root" — a generic name that works for any gallery.
	RootName string

	// pathPrefix is the resolved path prefix (after Provision
	// applies the default of filepath.Base(Root) if PathPrefix
	// is empty). This is what gets passed to RenderPage as the
	// breadcrumb's root label.
	pathPrefix string

	// rootName is the resolved display name for the first
	// breadcrumb segment (the gallery's root). Defaults to
	// "media root" if the operator didn't set RootName in
	// the Caddyfile (per user request 2026-06-20).
	rootName string

	// Sort is the field used to order the gallery. Valid values:
	//   "mtime" (default) — newest first
	//   "name"           — alphabetical
	Sort string

	// Template is the name of the template file to use, relative to
	// the templates dir (, default
	// /etc/caddy/gallery-templates). If empty, defaults to
	// "gallery.tmpl". The path is validated at Provision to
	// reject absolute paths and any traversal above the templates
	// dir (no  allowed). The template dir is the chroot; the
	// operator can only reference files inside it.
	Template string

	// ImageExts is the set of file extensions the gallery treats
	// as images. Set from the  Caddyfile subdirective
	// (space-separated, case-insensitive, with or without leading
	// dot). If empty (the default), the plugin uses a built-in list
	// of common image extensions (jpg, jpeg, png, gif, webp, svg,
	// avif, heic). Provision() converts this list to a map for the
	// Scanner to use.
	ImageExts []string

	// VideoExts is the set of file extensions the gallery treats
	// as videos. Set from the  Caddyfile subdirective
	// (same syntax as image_types). Empty (default) uses the
	// built-in video list (mp4, webm, m4v, mov, mkv, avi, ogv, ogg).
	VideoExts []string

	// NoThumbs disables the on-the-fly WebP thumbnail generation.
	// When true, the gallery uses the original image as the tile
	// <img src> instead of . Requests to the
	// thumb URL fall through to the next handler (typically
	// file_server, which 404s since no _thumbs/ dir exists).
	// Tradeoffs: no thumb cache, no CPU cost, but the browser
	// downloads the full image per tile (bigger page payload, slower
	// load on dirs of large photos). Useful for small galleries
	// where the operator doesn't want to maintain a thumb cache.
	NoThumbs bool

	// NoVideoThumbs disables the on-demand WebP thumbnail generation
	// for VIDEO files (extracted from the first frame via ffmpeg).
	// When true, videos still display in the gallery (with the
	// placeholder gradient + play button on each tile) but no
	// per-frame thumbnail is produced or served. When false (the
	// default), video thumbnails ARE generated IF ffmpeg is
	// available on the host — if ffmpeg is missing, video thumbs
	// fall back to the placeholder regardless of this setting
	// (there's no way to generate a frame without a tool that can
	// decode the video).
	// Caddyfile:  (no arg → true) or
	//             (re-enable).
	NoVideoThumbs bool

	// NoExif disables reading EXIF metadata from image files.
	// When true, the scanner skips the readExif call entirely
	// (no I/O, no parsing) — FileInfo.Exif is left nil for
	// all files. The card overlay then shows no "EXIF" pill
	// (the pill only renders when Exif is non-nil) and the
	// lightbox shows no EXIF panel. When false (the default),
	// EXIF is read for every image file at scan time (EAGER
	// loading — see scanner.go and exif.go).
	//
	// Per user request 2026-06-29: the Caddyfile operator can
	// disable EXIF entirely if they don't want the camera
	// metadata surfaced in the gallery. Useful for:
	//   - Privacy-sensitive deployments (no camera info exposed)
	//   - Performance: skip the per-image EXIF read (~1-5ms each)
	//   - Galleries that only need dimensions / thumbnails
	// Note that EXIF does NOT include GPS by default (see
	// exif.go — GPS is never extracted), so this is mainly
	// for the camera/lens/exposure metadata.
	// Caddyfile: no_exif (no arg → true) or no_exif false (re-enable).
	NoExif bool

	// ffmpegPath is the absolute path to the ffmpeg binary, set
	// in Provision. Empty when ffmpeg is not installed (or when
	// NoVideoThumbs is true — we skip the lookup since it would
	// be unused). Resolution order:
	//   1. FFMPEG_PATH env var (if set + points to an executable)
	//   2. exec.LookPath("ffmpeg") (scans /usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin)
	// Thread-safe to read after Provision returns; written only
	// during Provision.
	ffmpegPath string

	// imageExtsMap is the resolved image-extension set (after
	// lowercasing + dot-normalization in Provision) for fast
	// lookup in Scanner.Classify. Built from ImageExts (if
	// non-empty) or defaultImageExts. Set once in Provision;
	// read-only after that.
	imageExtsMap map[string]bool

	// videoExtsMap is the resolved video-extension set, same
	// shape as imageExtsMap.
	videoExtsMap map[string]bool

	// PageSize is the number of image entries per page. Default
	// is 50 (set in Provision if zero). The user can override
	// per-route via the Caddyfile: .
	// Validation: must be > 0. A zero or negative value is rejected
	// by UnmarshalCaddyfile (the Caddyfile parser).
	PageSize int
	// PageSizes is the list of page sizes the visitor can
	// choose from in the per-page dropdown (e.g. [30, 60, 120,
	// "all"]). Set in the Caddyfile via
	// (space-separated; "all" as a token means "show all items
	// in one page" - only included if explicitly listed).
	// If empty (default), the resolved PageSizes (set in
	// Provision) is [30, 60, 120, "all"]. The CURRENT page size
	// is set via the URL ?page_size=N parameter, which is
	// validated against this list (unknown values fall back to
	// the first item).
	PageSizes []string

	// SearchMatch controls how filenames are matched against
	// the search query. Two values:
	//   "substring" (default) — the query can match anywhere in
	//     the filename. "cat" matches "scatter.png".
	//   "word" — the query must match the start of a word
	//     boundary. "cat" matches "cat.jpg" and "my_cat.webp"
	//     but NOT "scatter.png". Uses the same word boundaries
	//     as the URL/PATH separators (_, -, space).
	//
	// Caddyfile:  (or omit for default).
	// Validation: must be one of the two; any other value
	// defaults silently to "substring" in Provision.
	SearchMatch string

	// ThumbWidth is the maximum width in pixels of generated
	// thumbnails. The source image is fit-within-bounds (aspect
	// ratio preserved, longest edge becomes the configured value).
	// Default: 320. Caddyfile: .
	// Validation: must be > 0; zero/negative rejected.
	ThumbWidth int

	// ThumbHeight is the maximum height in pixels of generated
	// thumbnails. Fit-within-bounds behavior — see ThumbWidth.
	// Default: 320. Caddyfile: .
	// Validation: must be > 0.
	ThumbHeight int

	// ThumbFormat is the output format for generated thumbnails.
	// One of: "jpeg" (or "jpg"), "png", or "webp" (the current
	// default, lossless). Default: "webp". Caddyfile:
	// . Validation: must be one of the three.
	ThumbFormat string

	// CacheScanMinutes is the in-memory scan cache TTL in
	// minutes. Default: 1. Caddyfile: .
	// Validation: must be > 0.
	CacheScanMinutes int

	// ThumbTTLMinutes is the HTTP Cache-Control max-age in
	// minutes for thumb responses. Thumbs are immutable per
	// source mtime, so a long TTL is safe. Default: 1440
	// (= 24 hours, matches the previous 86400-second value).
	// Caddyfile: . Validation: must be > 0.
	ThumbTTLMinutes int
	// MaxCacheSizeMB is the on-disk thumb cache size cap in
	// MB. When the cache directory exceeds this size, the
	// oldest thumbs (by file mtime) are evicted until the
	// cache is at 80% of the cap (20% headroom to avoid
	// thrashing). Default: 1024 (= 1 GB). Set to 0 to
	// disable the cap entirely (unbounded cache — current
	// pre-feature behavior).
	//
	// The cap is enforced by:
	//   1. An on-write check after each thumb is written
	//      (cheap, runs in a goroutine, doesn't block the
	//      request that triggered the cache write).
	//   2. A background sweep every 30 minutes (catches the
	//      case where the cache grows without new writes).
	//   3. An initial sweep at startup if the cache is
	//      already over the cap (so the operator's existing
	//      over-cap cache gets trimmed down on the first
	//      restart after they set the cap).
	//
	// Eviction policy: FIFO by file mtime. The cache file
	// names are sha256(source path) — opaque hex, not
	// sorted. We use the file's mtime on disk (which is the
	// WRITE time) to determine the oldest. For a true LRU,
	// enable filesystem atime or use a separate LRU log.
	MaxCacheSizeMB int
	// MaxCacheSizeSet is true when the operator explicitly
	// set max_cache_size_mb in the Caddyfile (including
	// the value 0). Used to distinguish "operator set 0
	// (no cap)" from "operator didn't set the directive
	// (use the default)". The default (when neither
	// Caddyfile nor JSON sets it) is 1024 MB.
	MaxCacheSizeSet bool

	// Cache holds the in-memory scan cache. Initialised in Provision
	// if nil. Excluded from JSON config (runtime state only).
	Cache *ScanCache
	// cacheSweepStop signals the background cache eviction
	// goroutine to stop. Closed by Cleanup so the goroutine
	// exits cleanly when Caddy shuts down. nil if no sweep
	// goroutine is running (e.g. when the cap is disabled).
	// Excluded from JSON config (runtime state only).
	cacheSweepStop chan struct{}
	// cacheStatsRefreshStop signals the 30-sec stats-refresh
	// goroutine to stop. Closed by Cleanup. nil if no
	// goroutine is running.
	cacheStatsRefreshStop chan struct{}
	// CacheStatsTracker records evictions and exposes the
	// most recent cacheStats snapshot via atomic.Pointer.
	// Initialised in Provision. Used by evictIfOver
	// (recordEvictions) and the stats-refresh goroutine
	// (snapshot + atomic swap). Excluded from JSON config.
	CacheStatsTracker *cacheStatsTracker
}

func (Gallery) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.media_gallery",
		New: func() caddy.Module { return new(Gallery) },
	}
}

// Provision sets up the module. Creates a default scan cache if one
// isn't already set.
func (g *Gallery) Provision(caddy.Context) error {
	if g.Cache == nil {
		g.Cache = NewScanCache(time.Duration(g.CacheScanMinutes) * time.Minute)
	}
	// Validate the configured template name. Must be relative and
	// must not traverse above the templates dir. Fail Caddy
	// startup on a bad value so a misconfiguration is caught at
	// boot, not at first request.
	if _, err := sanitizeTemplateName(g.Template); err != nil {
		return fmt.Errorf("invalid media_gallery template name %q: %w", g.Template, err)
	}
	// Apply defaults for the Caddyfile-only fields. Zero or
	// empty means "use the default" — the UnmarshalCaddyfile
	// already rejects invalid values, so if we see zero/empty
	// here it just means the user didn't specify a value.
	// Note: g.PageSize default is set LATER in Provision
	// (after the operator's page_size list is resolved),
	// so the first item in page_size becomes the default.
	if g.ThumbWidth == 0 {
		g.ThumbWidth = 320
	}
	if g.ThumbHeight == 0 {
		g.ThumbHeight = 320
	}
	if g.ThumbFormat == "" {
		g.ThumbFormat = "webp"
	}
	if g.CacheScanMinutes == 0 {
		g.CacheScanMinutes = 1
	}
	if g.ThumbTTLMinutes == 0 {
		g.ThumbTTLMinutes = 1440 // 24 hours, matches the previous 86400s
	}
	// Per user request 2026-06-27: cap the on-disk thumb
	// cache at 1 GB by default. Operators can override
	// with `max_cache_size_mb N` or explicitly disable
	// with `max_cache_size_mb 0`. The 1 GB default covers
	// the common case (~10,000 thumbs at 100 KB each, or
	// ~100,000 thumbs at 10 KB each with lossy WebP).
	//
	// MaxCacheSizeSet distinguishes "operator didn't set
	// the directive" (use the 1 GB default) from
	// "operator explicitly set 0" (no cap). The Caddyfile
	// parser sets MaxCacheSizeSet=true whenever the
	// directive appears, even with value 0.
	if !g.MaxCacheSizeSet {
		g.MaxCacheSizeMB = 1024
	}
	// Resolve the page sizes list (default [30, 60, 120, "all"]).
	// The operator can override via the  Caddyfile
	// directive (space-separated list). The FIRST item in the
	// list is the default page size (used if the URL doesn't
	// include ?page_size=). If they include "all" it means
	// "show all items on one page" - only included in the
	// dropdown if explicitly listed.
	if len(g.PageSizes) == 0 {
		g.PageSizes = []string{"60", "30", "120", "all"}
	}
	// The default PageSize is the first item in the
	// operator's DECLARED list (NOT the sorted list). So
	//  makes the default 60 even
	// though the dropdown displays the values in sorted
	// order (30 / 60 / 120 / all). If the operator didn't
	// set page_size at all, fall back to 30 (the default
	// list's first item).
	if g.PageSize == 0 {
		if len(g.PageSizes) > 0 {
			first := g.PageSizes[0]
			if first == "all" {
				g.PageSize = 0
			} else if n, err := strconv.Atoi(first); err == nil && n > 0 {
				g.PageSize = n
			} else {
				g.PageSize = 60
			}
		} else {
			g.PageSize = 60
		}
	}
	// Sort the page sizes: numeric values ascending, then "all"
	// at the end. This way the operator can write the list in
	// any order; the display is consistent. The default
	// (PageSize) is set ABOVE from the unsorted list, so
	// sorting the display doesn't change the default.
	g.PageSizes = sortPageSizes(g.PageSizes)
	// Resolve the search match mode (default "substring").
	// The operator can override via the  Caddyfile
	// directive (one of: "substring", "word"). Empty or
	// invalid values are silently treated as "substring" —
	// the documented default. We already validated the value
	// in UnmarshalCaddyfile, so reaching this point with
	// an empty string means the operator didn't set the
	// directive at all (use the default). Unknown values
	// would have been rejected at parse time, so we only
	// need to default empty here.
	if g.SearchMatch == "" {
		g.SearchMatch = "substring"
	}
	// Resolve the image + video extension sets. If the operator
	// configured them via the Caddyfile, use their list;
	// otherwise fall back to the built-in defaults. The resolved
	// maps are passed to ScanCache.Get and used by Scanner.Classify.
	// Resolve the path prefix. If PathPrefix is set in the
	// Caddyfile, use it; otherwise default to the basename
	// of the gallery's root directory. For example, if the
	// Caddyfile mounts the gallery at /images/* with root
	// /var/www/html/images, the default pathPrefix is "images".
	// This is what gets passed to RenderPage as the breadcrumb's
	// root label.
	g.pathPrefix = g.PathPrefix
	if g.pathPrefix == "" {
		g.pathPrefix = filepath.Base(g.Root)
	}
	// Resolve the breadcrumb root name. If the operator
	// configured  in the Caddyfile, use it;
	// otherwise default to the basename of the gallery's
	// root directory (same default as the path prefix).
	g.rootName = g.RootName
	if g.rootName == "" {
		g.rootName = filepath.Base(g.Root)
	}

	g.imageExtsMap = defaultImageExts
	if len(g.ImageExts) > 0 {
		g.imageExtsMap = extsToMap(g.ImageExts)
	}
	g.videoExtsMap = defaultVideoExts
	if len(g.VideoExts) > 0 {
		g.videoExtsMap = extsToMap(g.VideoExts)
	}
	// Detect ffmpeg for video thumbnail generation. We do this
	// once at Provision (not per-scan) since ffmpeg availability
	// doesn't change at runtime. If ffmpeg is missing OR
	// NoVideoThumbs is true, g.ffmpegPath stays empty and the
	// video-thumb code path falls back to the placeholder.
	//
	// Resolution order (Phase 67):
	//   1. FFMPEG_PATH env var (if set and points to an executable)
	//   2. exec.LookPath("ffmpeg") (scans /usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin)
	// If both fail, g.ffmpegPath stays empty and the video-thumb
	// code path falls back to the placeholder.
	if !g.NoVideoThumbs {
		if path := os.Getenv("FFMPEG_PATH"); path != "" {
			// Verify the env var actually points to an executable.
			// (We don't want to silently store a bad path that
			// would fail at request time with a confusing error.)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				if info.Mode()&0o111 != 0 {
					g.ffmpegPath = path
				}
			}
		}
		if g.ffmpegPath == "" {
			if path, err := exec.LookPath("ffmpeg"); err == nil {
				g.ffmpegPath = path
			}
		}
		// Per Phase 106: log the resolved ffmpeg path so the operator
		// can confirm the right binary was picked up at startup.
		// (The path is cached and reused for every video thumb request
		// thereafter — see docs/01-configuration.md for the
		// restart-after-install rationale.)
		if g.ffmpegPath != "" {
			fmt.Fprintf(os.Stderr, "caddy-media-gallery: ffmpeg path: %s\n", g.ffmpegPath)
		} else if !g.NoVideoThumbs {
			fmt.Fprintf(os.Stderr, "caddy-media-gallery: ffmpeg NOT FOUND (video thumbnails disabled; set FFMPEG_PATH or install ffmpeg and restart Caddy)\n")
		}
	}
	// Make the bundled templates discoverable on disk for the
	// operator. writeBundledTemplates is a no-op if the files
	// already exist (operator overrides preserved), and a
	// non-fatal error here doesn't block the module from serving
	// (the bundled templates still work).
	if err := writeBundledTemplates(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: caddy-media-gallery: could not write bundled templates to disk: %v\n", err)
	}

	// Per user request 2026-06-27: cap the thumb cache.
	// (1) Initial sweep at startup if the cache is already
	// over the cap. This brings an oversized cache down to
	// size on the first restart after the operator sets the
	// cap. Runs in a goroutine so Provision returns quickly.
	// (2) Background sweep every 30 minutes as a safety
	// net (catches the case where the cache grows without
	// new thumb writes, e.g. all visits are to cached thumbs).
	//
	// No-op if MaxCacheSizeMB is 0 (the explicit "no cap"
	// opt-out). The ticker is stopped by Cleanup.
	// Initialise the cache stats tracker. We ALWAYS create
	// one (even when MaxCacheSizeMB == 0) so the footer can
	// show current size + file count without a cap. With
	// MaxCacheSizeMB == 0, the eviction calls are no-ops
	// and the peaks stay at 0 — the footer renders the
	// infinity symbol for XX and 00 for YY/ZZ/AA.
	if g.CacheStatsTracker == nil {
		g.CacheStatsTracker = newCacheStatsTracker(g.MaxCacheSizeMB)
	}
	if g.MaxCacheSizeMB > 0 {
		cacheDir := g.thumbCacheDir()
		// Initial sweep at startup brings an oversized
		// cache down to size. Runs once.
		go func() {
			evictIfOver(cacheDir, g.MaxCacheSizeMB, g.CacheStatsTracker)
		}()
		g.cacheSweepStop = make(chan struct{})
		go g.cacheSweepLoop(cacheDir, g.MaxCacheSizeMB, g.cacheSweepStop)
	}
	// Stats-refresh goroutine: refreshes the snapshot every
	// 30 sec (one os.ReadDir walk + compute the three peaks
	// from the in-memory events list). Always running —
	// even when MaxCacheSizeMB == 0, we still want the
	// footer to show the current cache size and file count.
	g.cacheStatsRefreshStop = make(chan struct{})
	go g.cacheStatsRefreshLoop(g.CacheStatsTracker, g.cacheStatsRefreshStop)

	return nil
}

// cacheSweepLoop runs evictIfOver on a 30-minute ticker.
// Stopped by closing cacheSweepStop (called from Cleanup).
func (g *Gallery) cacheSweepLoop(cacheDir string, maxMB int, stop chan struct{}) {
	// Run the first sweep after 30 min, not immediately —
	// the initial sweep at startup already covered the
	// "clean up the existing cache" case.
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			evictIfOver(cacheDir, maxMB, g.CacheStatsTracker)
		}
	}
}

// cacheStatsRefreshLoop refreshes the cacheStats snapshot
// every 30 sec. Stops via cacheStatsRefreshStop (closed
// by Cleanup). Always runs (regardless of whether the
// eviction cap is enabled), because the visitor's footer
// shows the current size and file count even with the cap
// disabled (XX becomes ∞ in that case).
//
// Cost: one os.ReadDir + one os.Stat per file in the cache.
// For a 1 GB cache with ~5,000 thumbs, that's ~50 ms on
// an SSD. Negligible.
func (g *Gallery) cacheStatsRefreshLoop(tracker *cacheStatsTracker, stop chan struct{}) {
	cacheDir := g.thumbCacheDir()
	capMB := g.MaxCacheSizeMB
	// Refresh immediately so the first page render after
	// startup shows a populated footer (rather than zeros
	// for the first 30 sec).
	tracker.snapshot(cacheDir, capMB)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			tracker.snapshot(cacheDir, capMB)
		}
	}
}

func (g *Gallery) Cleanup() error {
	// Per user request 2026-06-27: stop the background
	// cache eviction sweep (if running). Closing the
	// channel signals the goroutine to exit. The
	// goroutine then returns and the ticker is stopped
	// via defer in cacheSweepLoop.
	if g.cacheSweepStop != nil {
		close(g.cacheSweepStop)
		g.cacheSweepStop = nil
	}
	// Also stop the stats-refresh loop. Same close(channel)
	// pattern. The tracker is kept in memory until the
	// Gallery struct is GC'd — visitors don't read it
	// anymore at this point so no race.
	if g.cacheStatsRefreshStop != nil {
		close(g.cacheStatsRefreshStop)
		g.cacheStatsRefreshStop = nil
	}
	return nil
}
func (*Gallery) Validate() error { return nil }

// ServeHTTP renders the gallery for the directory at the current
// request path, or falls through to the next handler (typically
// file_server) if the request is for a file.
//
// Path semantics after handle_path /images/* strips the prefix:
//
//	r.URL.Path = ""                    → render gallery for root
//	r.URL.Path = "subdir"              → render gallery for subdir
//	r.URL.Path = "subdir/"             → render gallery for subdir
//	r.URL.Path = "photo.jpg"           → fall through to file_server
//	r.URL.Path = "subdir/photo.jpg"    → fall through to file_server
//	r.URL.Path = "_thumbs/photo.webp"  → serve as thumbnail
//	r.URL.Path = "subdir/_thumbs/x.webp" → serve as thumbnail in subdir
//
// On transient errors (scan failures, template parse), it falls
// through to the next handler rather than returning a 500.
func (g *Gallery) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	root := g.Root
	if root == "" {
		if v, ok := caddyhttp.GetVar(r.Context(), "root").(string); ok && v != "" {
			root = v
		}
	}
	if root == "" {
		http.Error(w, "media_gallery: no root configured (set Gallery.Root in JSON or use  in the Caddyfile)", http.StatusInternalServerError)
		return nil
	}
	// Normalise the path. r.URL.Path may or may not have a leading
	// slash depending on Caddy's handle_path internals; we strip it
	// so filepath.Join behaves correctly and the resulting path is
	// relative to the gallery root.
	relPath := strings.TrimPrefix(r.URL.Path, "/")
	resolved := filepath.Join(root, relPath)

	// Thumb requests get a special handler. It resolves the source
	// file in (root + subdir) for the path BEFORE the _thumbs/
	// segment, so /subdir/_thumbs/photo.webp looks up
	// (root/subdir/photo.<ext>).
	//
	// When no_thumbs is enabled, we skip this branch entirely.
	// The request falls through to the next handler (file_server
	// or whatever) which 404s because no _thumbs/ dir exists on
	// disk. This is the correct behavior — the thumb URL just
	// doesn't exist for galleries that don't generate thumbs.
	if !g.NoThumbs {
		if g.serveThumb(w, r, root, relPath) {
			return nil
		}
	}

	// If the resolved path exists and is a regular file, fall
	// through to file_server (or whatever the next handler is).
	if info, err := os.Stat(resolved); err == nil && !info.IsDir() {
		return next.ServeHTTP(w, r)
	}
	// If the path doesn't exist at all, fall through so file_server
	// returns a real 404 (not a 200 with an empty-gallery page).
	if _, err := os.Stat(resolved); err != nil {
		return next.ServeHTTP(w, r)
	}

	// Path is a directory. If the request URL doesn't end with a
	// trailing slash, 301-redirect to the canonical form so the
	// browser resolves relative URLs (./_thumbs/photo.webp) the
	// same way it does for the trailing-slash version. This matches
	// what file_server does for directory indexes; without it,
	// visiting /images/foo resolves ./ against /images/ instead of
	// /images/foo/, breaking the thumb URLs.
	//
	// We use a RELATIVE Location (no leading slash) because
	// Caddy's handle_path rewrites both r.URL.Path AND
	// r.RequestURI, so we can't reconstruct the full original
	// URL from inside the handler. A relative reference is
	// resolved by the browser against the current request URL
	// per RFC 3986 §5.2 — "generated/" against base
	// "/images/generated" yields "/images/generated/" via the
	// merge algorithm, regardless of whether the browser
	// treats the base as a file or a directory.
	//
	// We set the Location header manually instead of using
	// http.Redirect() because the latter normalises the
	// location to an absolute path (prepending "/"), which the
	// browser would then resolve against the host root instead
	// of the request URL.
	if relPath != "" && !strings.HasSuffix(relPath, "/") {
		w.Header().Set("Location", relPath+"/")
		w.WriteHeader(http.StatusMovedPermanently)
		return nil
	}

	// It's a directory. Scan it and render the gallery.
	files, err := g.Cache.Get(resolved, g.Sort, g.imageExtsMap, g.videoExtsMap, g.NoExif)
	if err != nil {
		// Scan failure (permission denied, etc.) — fall through.
		return next.ServeHTTP(w, r)
	}
	// Title: basename of the resolved dir, falling back to the
	// gallery root for the top-level case.
	title := filepath.Base(resolved)
	if title == "." || title == "" {
		title = filepath.Base(root)
	}
	// Per user request 2026-06-27: format the cache stats
	// snapshot into the four hex strings for the footer.
	// XX is the cache usage percent (00-FF) or "∞" if
	// unbounded. YY/ZZ/AA are peak eviction counts per
	// hour bucket, clamped to 0xFF so the hex is always
	// two digits. Read the snapshot via atomic load (no
	// lock contention with the eviction goroutine).
	stats := g.CacheStatsTracker.load()
	cacheXX, cacheYY, cacheZZ, cacheAA := formatCacheStatsFooter(stats)
	body, err := RenderPage(title, "./", "./_thumbs/", relPath, g.Template, g.NoThumbs, g.NoVideoThumbs, g.PageSize, g.PageSizes, files, r.URL.Query(), g.imageExtsMap, g.videoExtsMap, g.rootName, g.PathPrefix, g.SearchMatch, cacheXX, cacheYY, cacheZZ, cacheAA)
	if err != nil {
		http.Error(w, "media_gallery: render failed: "+err.Error(), http.StatusInternalServerError)
		return nil
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(body))
	return nil
}

func (g *Gallery) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	g.Sort = "mtime"
	for d.Next() {
		for d.NextBlock(0) {
			switch d.Val() {
			case "sort":
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.Sort = d.Val()
				if g.Sort != "mtime" && g.Sort != "name" {
					return d.ArgErr()
				}
			case "template":
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.Template = d.Val()
				if d.NextArg() {
					return d.ArgErr()
				}
			case "no_thumbs":
				g.NoThumbs = true
				if d.NextArg() {
					if d.Val() != "false" {
						return d.ArgErr()
					}
					g.NoThumbs = false
				}
			case "no_video_thumbs":
				g.NoVideoThumbs = true
				if d.NextArg() {
					if d.Val() != "false" {
						return d.ArgErr()
					}
					g.NoVideoThumbs = false
				}

		case "no_exif":
			// Per user request 2026-06-29: operator can
			// disable EXIF entirely. When true, the scanner
			// skips the readExif call entirely (no I/O,
			// no parsing). The EXIF pill on cards and the
			// EXIF panel in the lightbox are skipped
			// automatically because they only render
			// when FileInfo.Exif is non-nil. Usage:
			//   no_exif           # disable
			//   no_exif false     # re-enable
			g.NoExif = true
			if d.NextArg() {
				if d.Val() != "false" {
					return d.ArgErr()
				}
				g.NoExif = false
			}

			case "page_size":
				// Per user request 2026-06-27: the operator
				// configures the per-page dropdown options via
				// `page_size 60 30 120 all` (space-separated).
				// The FIRST item in the list is the DEFAULT
				// page size (used if the URL doesn't include
				// ?page_size=). "all" is a special token
				// meaning "show all items on one page" - only
				// included in the dropdown if the operator
				// explicitly listed it. The dropdown is
				// always sorted by increasing value with "all"
				// at the end (so the operator can write the
				// list in any order; the display is consistent).
				// Examples:
				//   page_size 30 60 120 all
				//   page_size 50 100
				//   page_size 25 50 75 100
				// If the operator doesn't set page_size,
				// the default is [30, 60, 120, "all"] (per
				// user request 2026-06-20).
				//
				// This replaces the old single-value form
				// (page_size N). The list form is strictly
				// more capable (the list IS the default
				// ordering).
				g.PageSizes = nil
				for d.NextArg() {
					g.PageSizes = append(g.PageSizes, d.Val())
				}
				if len(g.PageSizes) == 0 {
					return d.ArgErr()
				}
			case "search_match":
				// Per user request 2026-06-27: operator
				// configures the filename matching rule
				// for the search feature. Two values:
				//   "substring" (default) — query can
				//     match anywhere in the filename
				//   "word" — query must match the
				//     start of a word boundary
				// If the operator doesn't set this, or
				// sets an invalid value, the resolved
				// value in Provision is "substring".
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.SearchMatch = d.Val()
				if g.SearchMatch != "substring" && g.SearchMatch != "word" {
					return d.ArgErr()
				}
				if d.NextArg() {
					return d.ArgErr()
				}

			case "thumb_width":
				if !d.NextArg() {
					return d.ArgErr()
				}
				n, err := strconv.Atoi(d.Val())
				if err != nil || n <= 0 {
					return d.ArgErr()
				}
				g.ThumbWidth = n
				if d.NextArg() {
					return d.ArgErr()
				}
			case "thumb_height":
				if !d.NextArg() {
					return d.ArgErr()
				}
				n, err := strconv.Atoi(d.Val())
				if err != nil || n <= 0 {
					return d.ArgErr()
				}
				g.ThumbHeight = n
				if d.NextArg() {
					return d.ArgErr()
				}
			case "thumb_format":
				if !d.NextArg() {
					return d.ArgErr()
				}
				format := d.Val()
				if format != "jpeg" && format != "jpg" && format != "png" && format != "webp" {
					return d.ArgErr()
				}
				g.ThumbFormat = format
				if d.NextArg() {
					return d.ArgErr()
				}
			case "cache_scan":
				if !d.NextArg() {
					return d.ArgErr()
				}
				n, err := strconv.Atoi(d.Val())
				if err != nil || n <= 0 {
					return d.ArgErr()
				}
				g.CacheScanMinutes = n
				if d.NextArg() {
					return d.ArgErr()
				}
			case "thumb_ttl":
				if !d.NextArg() {
					return d.ArgErr()
				}
				n, err := strconv.Atoi(d.Val())
				if err != nil || n <= 0 {
					return d.ArgErr()
				}
				g.ThumbTTLMinutes = n
			case "max_cache_size_mb":
				// Per user request 2026-06-27: cap the on-disk
				// thumb cache size. Prevents runaway growth
				// on galleries with many images. Default: 1024
				// (1 GB). Set to 0 to disable the cap
				// entirely (unbounded — the pre-feature
				// behavior). Validation: >= 0.
				//
				// MaxCacheSizeSet is set to true even when
				// the value is 0, so Provision can
				// distinguish "operator set 0" from
				// "operator didn't set the directive" (use
				// the 1 GB default).
				if !d.NextArg() {
					return d.ArgErr()
				}
				n, err := strconv.Atoi(d.Val())
				if err != nil || n < 0 {
					return d.ArgErr()
				}
				g.MaxCacheSizeMB = n
				g.MaxCacheSizeSet = true
				if d.NextArg() {
					return d.ArgErr()
				}
			case "image_types":
				// Space-separated list of image extensions. Empty
				// args are silently skipped; entries are normalized
				// to ".ext" + lowercase by extsToMap() in Provision.
				// Examples:
				//   image_types jpg jpeg png
				//   image_types .jpg .png .heic   (leading dot allowed)
				//   image_types JPG JPEG PNG      (case-insensitive)
				g.ImageExts = nil
				for d.NextArg() {
					g.ImageExts = append(g.ImageExts, d.Val())
				}
			case "video_types":
				// Same shape as image_types. Empty args skipped.
				//   video_types mp4 webm mov
				g.VideoExts = nil
				for d.NextArg() {
					g.VideoExts = append(g.VideoExts, d.Val())
				}
			case "path_prefix":
				// URL mount path for the gallery, used by the
				// breadcrumb to build absolute links. Example:
				// if the gallery is mounted at /images/* in the
				// Caddyfile, set
				//   path_prefix /images/
				// so the breadcrumb's first segment is "images"
				// (links to "/images/") and subsequent segments
				// are absolute URLs like "/images/media_gallery/".
				// If empty (default), the breadcrumb uses relative
				// links (./seg/...) — backwards-compatible.
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.PathPrefix = d.Val()
				if d.NextArg() {
					return d.ArgErr()
				}
			case "root_name":
				// Display name for the first breadcrumb
				// segment (the gallery's root). Set in the
				// Caddyfile as e.g. . Used
				// by the breadcrumb to show a custom name
				// (like "images", "Photos", "My Gallery")
				// instead of the default "media root".
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.RootName = d.Val()
				if d.NextArg() {
					return d.ArgErr()
				}
			}
		}
	}
	return nil
}

// Compile-time interface compliance
var (
	_ caddyfile.Unmarshaler       = (*Gallery)(nil)
	_ caddyhttp.MiddlewareHandler = (*Gallery)(nil)
)

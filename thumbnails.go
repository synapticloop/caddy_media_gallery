package gallery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/HugoSmits86/nativewebp"
	"golang.org/x/image/draw"
)


// imageExtsForThumb is the set of source extensions we will generate
// thumbnails for. Videos are excluded — they get a play-button overlay
// in the template instead of a thumb image.
var imageExtsForThumb = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true,
	".gif": true, ".webp": true, ".avif": true, ".svg": true,
}

// videoExtsForThumb is the set of file extensions for which a
// first-frame thumbnail can be extracted via ffmpeg. Used by
// findSourceForThumb to detect video sources.
//
// Note: webm and mov are container formats; the actual codecs
// inside them matter to ffmpeg but not to us — ffmpeg will pick
// up the demuxer from the file's magic bytes regardless of
// extension. So this list covers all the common video formats
// visitors are likely to encounter.
//
// We don't include every format ffmpeg supports (there are many
// obscure ones); we cover the realistic set.
var videoExtsForThumb = map[string]bool{
	".mp4": true, ".m4v": true, ".webm": true, ".mov": true,
	".mkv": true, ".avi": true, ".ogv": true, ".ogg": true,
}

// ThumbConfig holds the runtime configuration for thumb
// generation. Set at Provision time from the Gallery's
// Caddyfile-configured values (or defaults).
type ThumbConfig struct {
	// Width and Height are the max-dim bounding box (in pixels)
	// for the generated thumb. The source image is fit-within-
	// bounds: aspect ratio is preserved and the longest edge
	// becomes the configured value.
	Width  int
	Height int
	// Format is the output format: "jpeg" (or "jpg"), "png", or
	// "webp" (the default, lossless). Encoded with stdlib
	// image/jpeg (quality 75), stdlib image/png, or
	// github.com/HugoSmits86/nativewebp respectively.
	Format string
	// MaxCacheSizeMB is the configured cache cap (from
	// the Caddyfile). 0 = no cap (the pre-feature
	// behavior). Passed to the eviction helper after
	// each successful cache write.
	MaxCacheSizeMB int
	// CacheStatsTracker records eviction counts so the
	// footer can show peak evictions per period. nil is
	// safe (recordEvictions is a no-op). Set in
	// Provision; passed through ThumbConfig to
	// GenerateOrLoadThumb.
	CacheStatsTracker *cacheStatsTracker
}

// ThumbPath returns the on-disk path where the thumbnail for src
// should be cached. The filename is the first 16 bytes of the SHA256
// of src's absolute path, hex-encoded (32 hex chars) + ".webp".
// Using a content-hash means cache entries are stable across renames
// of the parent directory (as long as the absolute source path stays
// the same) and collisions are effectively impossible.
// ThumbPath returns the on-disk path where the thumbnail for src
// would be cached, using the default .webp extension. The layout
// is <cacheDir>/<aa>/<bb>/<rest>.webp (a 2-level hash directory
// tree — see cachePath for rationale). The hash is the first
// 16 bytes of SHA-256 of the absolute source path, hex-encoded
// (32 chars total). Using a content hash means the path is
// stable across renames of the parent directory (as long as
// the absolute path stays the same) and collisions are
// effectively impossible.
//
// ThumbPath is the "external" interface used by the tests and
// by other packages that need to know where a thumb lives.
// It's equivalent to thumbCachePath(src, cacheDir, "webp").
func ThumbPath(src, cacheDir string) string {
	return thumbCachePath(src, cacheDir, "webp")
}

// GenerateOrLoadThumb returns the thumbnail bytes for src, generating
// and caching on first call and serving from cache on subsequent
// calls. The cfg parameter (Width, Height, Format) controls the
// output size and output format. The source is fit-within-bounds
// (aspect ratio preserved, longest edge becomes the configured
// value). Sources already within the box are encoded without
// resizing.
//
// The cache file is at <cacheDir>/<sha256(absolute source path)>.<ext>
// keyed by (source path, cfg.Format) so changing the format
// invalidates the old cache automatically. The cache is also
// regenerated when the source mtime is newer than the cache
// mtime.
func GenerateOrLoadThumb(src, cacheDir string, cfg ThumbConfig) ([]byte, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	if cfg.Width <= 0 {
		cfg.Width = 320
	}
	if cfg.Height <= 0 {
		cfg.Height = 320
	}
	if cfg.Format == "" {
		cfg.Format = "webp"
	}
	// Map the format string to its on-disk file extension
	// (used in the cache filename and the served URL).
	ext := "webp"
	switch cfg.Format {
	case "jpeg", "jpg":
		ext = "jpg"
	case "png":
		ext = "png"
	case "webp":
		ext = "webp"
	default:
		return nil, fmt.Errorf("unsupported thumb format %q (use jpeg, png, or webp)", cfg.Format)
	}
	cacheFile := thumbCachePath(src, cacheDir, ext)
	srcInfo, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("stat source: %w", err)
	}
	// Cache lookup: try the new nested path first, then the
	// legacy flat layout (for caches populated before the
	// nested layout was introduced). readCacheFile does the
	// fallback + opportunistic migration.
	if data, _, found := readCacheFile(src, cacheDir, ext); found {
		cacheFile = thumbCachePath(src, cacheDir, ext)
		if cacheInfo, err := os.Stat(cacheFile); err == nil {
			// Cache hit: source must not be newer than cache.
			if !cacheInfo.ModTime().Before(srcInfo.ModTime()) {
				return data, nil
			}
		}
	}

	// Cache miss or stale: decode, resize, encode.
	f, err := os.Open(src)
	if err != nil {
		return nil, fmt.Errorf("open source: %w", err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode source: %w", err)
	}
	bounds := img.Bounds()
	thumb := img
	if bounds.Dx() > cfg.Width || bounds.Dy() > cfg.Height {
		// Fit-within-bounds: scale so the longest edge fits
		// in the cfg.Width × cfg.Height box, preserving aspect
		// ratio.
		scaleX := float64(cfg.Width) / float64(bounds.Dx())
		scaleY := float64(cfg.Height) / float64(bounds.Dy())
		scale := scaleX
		if scaleY < scaleX {
			scale = scaleY
		}
		newW := int(float64(bounds.Dx()) * scale)
		newH := int(float64(bounds.Dy()) * scale)
		if newW < 1 {
			newW = 1
		}
		if newH < 1 {
			newH = 1
		}
		dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
		thumb = dst
	}

	// Encode in the configured format.
	var buf bytes.Buffer
	switch cfg.Format {
	case "jpeg", "jpg":
		// Quality 75: a common default for thumbnails.
		// Smaller files than lossless WebP, larger than q=80
		// lossy WebP would be. Good middle ground for
		// galleries.
		if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: 75}); err != nil {
			return nil, fmt.Errorf("encode jpeg: %w", err)
		}
	case "png":
		if err := png.Encode(&buf, thumb); err != nil {
			return nil, fmt.Errorf("encode png: %w", err)
		}
	case "webp":
		// github.com/HugoSmits86/nativewebp is a pure-Go
		// lossless WebP/VP8L encoder. No CGO, no libwebp.
		// Lossless only — produces 2-3x larger files than
		// lossy q=80. For 320px gallery thumbs the size is
		// still manageable (typically 10-50KB per thumb).
		if err := nativewebp.Encode(&buf, thumb, &nativewebp.Options{
			CompressionLevel: nativewebp.BestCompression,
		}); err != nil {
			return nil, fmt.Errorf("encode webp: %w", err)
		}
	}
	// Write to cache (best-effort; a write error shouldn't fail
	// the request — the in-memory bytes are still good).
	// Per user request 2026-06-30: the new nested cache layout
	// uses a 2-level subdir. We MkdirAll the parent first
	// (no-op if the dir already exists).
	_ = os.MkdirAll(filepath.Dir(cacheFile), 0o755)
	_ = os.WriteFile(cacheFile, buf.Bytes(), 0o644)
	// Per user request 2026-06-29: set the thumb's mtime to
	// the source's mtime so the staleness check at the top of
	// this function works as "is the source newer than the
	// thumb?" — semantically clear. Without this, the thumb
	// has the time it was generated, which is always NOW
	// (never older than the source unless the source changes
	// after the thumb is generated). With this change, the
	// thumb's mtime mirrors the source's, so a touched
	// source will look newer than the cached thumb and
	// trigger a regeneration.
	//
	// The cache eviction logic does NOT use the thumb's mtime
	// anymore — it uses the .meta sidecar's mtime (which
	// is updated on each serve via touchMetaAtUse, acting
	// as a "last used" timestamp for proper LRU eviction).
	// See evictIfOver for details.
	_ = os.Chtimes(cacheFile, srcInfo.ModTime(), srcInfo.ModTime())

	// Per user request 2026-06-27: cap the on-disk thumb
	// cache. After each successful write, kick off a
	// fire-and-forget eviction (goroutine, non-blocking).
	// No-op if MaxCacheSizeMB is 0 (unbounded — the
	// pre-feature behavior).
	maybeEvictAsync(cacheDir, cfg.MaxCacheSizeMB, cfg.CacheStatsTracker)

	// Return a copy of the bytes (so callers can't mutate our
	// buffer).
	out := make([]byte, buf.Len())
	copy(out, buf.Bytes())
	return out, nil
}

// isVideoExt returns true if the given file path has a recognized
// video extension. Used by findSourceForThumb to decide whether
// to dispatch to GenerateOrLoadVideoThumb (when ffmpeg is
// available) vs the image-based GenerateOrLoadThumb.
func isVideoExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return videoExtsForThumb[ext]
}

// GenerateOrLoadVideoThumb extracts the first frame from a video
// using ffmpeg, saves it as a WebP thumbnail, and returns the
// thumbnail bytes. Mirrors GenerateOrLoadThumb's caching
// behavior: if a cache file already exists and is not older than
// the source video, the cached bytes are returned without
// invoking ffmpeg.
//
// The ffmpegPath argument must be the absolute path to a working
// ffmpeg binary (use Gallery.ffmpegPath, set in Provision via
// exec.LookPath). If ffmpegPath is empty, the function returns
// an error — callers should check gallery.VideoThumbsEnabled()
// first to know whether to call this function.
//
// ffmpeg invocation:
//
//	ffmpeg -y -i input.mp4 -vframes 1 -vf "scale=W:H:force_original_aspect_ratio=decrease" output.webp
//
// -y: overwrite output without prompting
// -i: input
// -vframes 1: extract exactly one frame
// -vf scale=W:H:force_original_aspect_ratio=decrease: fit-within-bounds
// output: webp (or other format if cfg.Format is set; we default to webp
// since that's what the rest of the thumb pipeline uses)
//
// Note on seeking: we use -vframes 1 which extracts the FIRST
// frame. For most videos this is fine. Some videos have an all-
// black opening frame (the "fade-in" frame); if that becomes a
// problem we can add -ss 0.5 to seek forward half a second.
func GenerateOrLoadVideoThumb(src, cacheDir, ffmpegPath string, cfg ThumbConfig) ([]byte, error) {
	if ffmpegPath == "" {
		return nil, fmt.Errorf("video thumb: ffmpeg path is empty")
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	// Same width/height defaults as GenerateOrLoadThumb.
	if cfg.Width <= 0 {
		cfg.Width = 320
	}
	if cfg.Height <= 0 {
		cfg.Height = 320
	}
	// We always write webp for video thumbs (regardless of the
	// configured image thumb format) because ffmpeg writes its
	// output in the format you specify in the output filename.
	// The thumb pipeline only knows how to serve .webp files;
	// jpeg/png would require a different cache path scheme.
	ext := "webp"
	cacheFile := thumbCachePath(src, cacheDir, ext)
	srcInfo, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("stat source: %w", err)
	}
	// Cache lookup: try the new nested path first, then the
	// legacy flat layout. readCacheFile does the fallback +
	// opportunistic migration.
	if data, _, found := readCacheFile(src, cacheDir, ext); found {
		cacheFile = thumbCachePath(src, cacheDir, ext)
		if cacheInfo, err := os.Stat(cacheFile); err == nil {
			if !cacheInfo.ModTime().Before(srcInfo.ModTime()) {
				return data, nil
			}
		}
	}
	// Per user request 2026-06-30: the new nested cache layout
	// uses a 2-level subdir (e.g. <cacheDir>/<aa>/<bb>/).
	// ffmpeg can't create the subdirs itself — it just opens
	// the output file and writes. So we MkdirAll the parent
	// subdir first. This is a no-op if the subdir already
	// exists (e.g. on a cache hit), and harmless if the
	// subdir creation fails (the subsequent ffmpeg call
	// will also fail with a clear error).
	_ = os.MkdirAll(filepath.Dir(cacheFile), 0o755)
	// Build the scale filter. force_original_aspect_ratio=decrease
	// scales so the longest edge fits in the box (matches the
	// image thumb behavior).
	scaleFilter := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", cfg.Width, cfg.Height)
	cmd := exec.Command(ffmpegPath,
		"-y",      // overwrite output without prompting
		"-i", src, // input
		"-vframes", "1", // extract exactly one frame
		"-vf", scaleFilter,
		cacheFile, // output (filename determines the format)
	)
	// Capture stderr for error messages (ffmpeg logs to stderr
	// by default).
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg failed for %s: %w (stderr: %s)", src, err, stderr.String())
	}
	// Confirm the output file exists (ffmpeg can exit 0 but
	// produce no output if the codec is unrecognized; rare but
	// possible).
	if _, err := os.Stat(cacheFile); err != nil {
		return nil, fmt.Errorf("ffmpeg produced no output for %s: %w", src, err)
	}
	// Per user request 2026-06-29: set the thumb's mtime to
	// the source's mtime (same reason as in
	// GenerateOrLoadThumb). This makes the staleness check
	// "is the source newer than the thumb?" semantically clear
	// and consistent across image and video thumbs.
	_ = os.Chtimes(cacheFile, srcInfo.ModTime(), srcInfo.ModTime())
	return os.ReadFile(cacheFile)
}


// EagerGenPageThumbs pre-generates the on-page thumbs for
// the visible files in paged, then spawns a background
// goroutine to generate the rest (offPage) so subsequent
// page navigations are also warm.
//
// Per user request 2026-07-01: when a directory's scan
// cache TTL has expired (or the directory is being
// cached for the first time), the browser has to wait
// for ~60 thumbs to be generated — that's ~6 seconds of
// latency on the first navigation. This helper generates
// the page-visible thumbs synchronously (in the request
// goroutine) so they're ready by the time the browser
// requests them, then finishes the rest in the background
// with a small rate limit to avoid thundering-herd against
// ffmpeg/the disk.
//
// paged: the files visible on the current page (sorted
//        and filter-applied). These get SYNCHRONOUS
//        pre-gen.
// offPage: all OTHER media files in the directory not
//          visible on the current page (so navigation
//          to other pages is also warm). These get
//          BACKGROUND pre-gen.
//
// cacheDir, ffmpegPath, cfg: the standard thumb-gen args.
// ffmpegPath may be "" — video thumbs are silently skipped
// in that case (the rest of the pipeline 404s them).
//
// IMPORTANT: this is best-effort. Any thumb-gen errors
// are silently ignored (the browser will just see a 404
// / lazy generate when it requests the thumbnail). The
// whole point of this helper is to AVOID that lazy-gen
// latency, not to guarantee it.
//
// Performance characteristics:
//
//   | Files   | Sync (paged)            | Background (offPage)     |
//   |---------|-------------------------|---------------------------|
//   | 60      | ~600 ms (10x parallel)  | rate-limited (~200 ms each)|
//   | 200     | ~600 ms                 | rate-limited (~40 sec)    |
//   | 1000    | ~600 ms                 | rate-limited (~3 min)     |
//
// The sync phase is bounded by the page size (default 60),
// not the directory size, so it's always "fast enough"
// even for huge directories.
func EagerGenPageThumbs(resolved string, paged, offPage []FileInfo, cacheDir, ffmpegPath string, cfg ThumbConfig) {
	if cfg.Width <= 0 {
		cfg.Width = 320
	}
	if cfg.Height <= 0 {
		cfg.Height = 320
	}
	if cfg.Format == "" {
		cfg.Format = "webp"
	}
	// Phase 1: synchronously generate the on-page thumbs.
	// Bounded parallelism: 10 concurrent thumb-gen workers
	// (most thumb gen is image-decode + resize, which is
	// CPU-bound; 10 is plenty on a modern multi-core box).
	generateThumbsForFiles(resolved, paged, cacheDir, ffmpegPath, cfg, 10)
	// Phase 2: background pre-gen for the off-page files.
	// Rate-limited (2 concurrent workers). The full sweep
	// can take a while for huge directories, but the user
	// is no longer waiting on it.
	if len(offPage) > 0 {
		go func(files []FileInfo) {
			generateThumbsForFiles(resolved, files, cacheDir, ffmpegPath, cfg, 2)
		}(offPage)
	}
}

// generateThumbsForFiles is the workhorse for EagerGenPageThumbs.
// It runs up to maxParallel thumb-gen operations concurrently,
// waiting for all to finish before returning. Errors are
// silently swallowed (this is best-effort).
func generateThumbsForFiles(resolved string, files []FileInfo, cacheDir, ffmpegPath string, cfg ThumbConfig, maxParallel int) {
	if len(files) == 0 {
		return
	}
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup
	for _, f := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(file FileInfo) {
			defer wg.Done()
			defer func() { <-sem }()
			src := filepath.Join(resolved, file.Name)
			// Skip video thumbs if no ffmpeg (matches serveThumb's
			// behavior).
			if isVideoExt(src) {
				if ffmpegPath == "" {
					return
				}
				_, _ = GenerateOrLoadVideoThumb(src, cacheDir, ffmpegPath, cfg)
				return
			}
			_, _ = GenerateOrLoadThumb(src, cacheDir, cfg)
		}(f)
	}
	wg.Wait()
}


// cachePath returns the on-disk path where the cache file for
// src should be cached, for a specific suffix (e.g. ".webp",
// ".webp.meta", ".webp.exif"). The path is:
//
//	<cacheDir>/<aa>/<bb>/<rest32>.<suffix>
//
// where <aa> and <bb> are the first two bytes of the hash
// (each 1 byte = 2 hex chars), and <rest32> is the remaining
// 30 hex chars. So the 32-hex-char hash is split as
// "aabb<rest>" and used as a 2-level nested subdir.
//
// Per user request 2026-06-30: at large cache sizes (e.g.
// 24,000+ files in one flat directory), filesystem lookups
// slow down because each stat/opendir call must scan the
// full directory. A 2-level hash layout keeps each subdir
// small (average ~0.4 entries for a 24k cache, max ~few
// hundred for a 100k+ cache) so the lookup is O(1) instead
// of O(n).
//
// The legacy flat layout is still readable: this function
// always returns the NEW nested path. The lookup logic in
// the cache functions (e.g. serveThumb, evictIfOver)
// checks the new nested location FIRST, then falls back to
// the old flat location for backward compatibility with
// existing caches. New writes always use the new nested
// layout. The eviction sweep moves old flat-layout files
// to their nested location on a best-effort basis (as part
// of its normal scan).
func cachePath(src, cacheDir, suffix string) string {
	abs, err := filepath.Abs(src)
	if err != nil {
		// Fall back to the raw path if Abs fails (shouldn't
		// happen in practice but avoids a panic).
		abs = src
	}
	h := sha256.Sum256([]byte(abs))
	hex := hex.EncodeToString(h[:16]) // 32 hex chars
	// Nested layout: <cacheDir>/<aa>/<bb>/<rest32>.<suffix>
	subdir1 := hex[:2]   // "aa"
	subdir2 := hex[2:4]  // "bb"
	rest := hex[4:]      // 28 hex chars
	return filepath.Join(cacheDir, subdir1, subdir2, rest+suffix)
}

// thumbCachePath returns the on-disk path where the thumbnail for
// src should be cached, for a specific output extension. See
// cachePath for the layout details.
func thumbCachePath(src, cacheDir, ext string) string {
	return cachePath(src, cacheDir, "."+ext)
}


// readCacheFile reads the cached thumb for src. Returns
// (data, path, true) if found, (nil, "", false) otherwise.
// Per user request 2026-06-30: the cache uses a 2-level
// nested hash layout. The legacy flat-layout fallback was
// removed because the entire cache is regenerated in the
// new layout on first start with the new code.
func readCacheFile(src, cacheDir, ext string) ([]byte, string, bool) {
	newPath := thumbCachePath(src, cacheDir, ext)
	if data, err := os.ReadFile(newPath); err == nil {
		return data, newPath, true
	}
	return nil, "", false
}

// findSourceForThumb looks up the source file for a thumb request.
// subdir is the directory portion of the thumb URL (may be empty
// for top-level thumbs), sourceRel is the thumb basename without
// the .webp extension. Returns the full source path if found, empty
// string if not. Tries each image extension in turn.
func findSourceForThumb(root, subdir, sourceRel string) string {
	dir := filepath.Join(root, subdir)
	for ext := range imageExtsForThumb {
		candidate := filepath.Join(dir, sourceRel+ext)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	// Try video extensions too (only reached if no image matched).
	// We check videos AFTER images so an image with the same
	// basename as a video would win (unlikely but possible; we
	// follow the existing extension-priority logic).
	for ext := range videoExtsForThumb {
		candidate := filepath.Join(dir, sourceRel+ext)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// serveThumb handles a /_thumbs/<name>.webp request: looks up the
// source, generates or loads the thumb, writes it as image/webp.
// Returns true if the request was handled, false if it wasn't a
// thumb request (caller should render the gallery).
//
// root is the gallery root, relPath is the request path relative to
// the root (no leading slash). The thumb URL's pattern is:
//
//	relPath = "<subdir>/_thumbs/<basename>.webp"
//
// or just "_thumbs/<basename>.webp" for the top-level gallery. The
// source file lives at (root/<subdir>/<basename>.<ext>).
func (g *Gallery) serveThumb(w http.ResponseWriter, r *http.Request, root, relPath string) bool {
	const prefix = "_thumbs/"
	idx := strings.Index(relPath, prefix)
	if idx < 0 {
		return false
	}
	// everything before "_thumbs/" is the subdirectory portion.
	subdir := relPath[:idx]
	rest := relPath[idx+len(prefix):]
	// rest is "photo.webp" (or "subdir/photo.webp" for nested
	// thumbs). Strip the .webp suffix to get the source basename,
	// keeping any nested subdir prefix.
	if !strings.HasSuffix(rest, ".webp") {
		http.NotFound(w, r)
		return true
	}
	sourceRel := strings.TrimSuffix(rest, ".webp")
	// Reject path traversal.
	if strings.Contains(sourceRel, "..") {
		http.NotFound(w, r)
		return true
	}
	if root == "" {
		http.Error(w, "media_gallery: no root configured", http.StatusInternalServerError)
		return true
	}
	src := findSourceForThumb(root, subdir, sourceRel)
	if src == "" {
		http.NotFound(w, r)
		return true
	}
	// Dispatch to the right thumb generator: image -> GenerateOrLoadThumb,
	// video -> GenerateOrLoadVideoThumb (only if ffmpeg is available
	// and not disabled by the directive; otherwise 404 — there's no
	// frame to serve).
	var data []byte
	var err error
	if isVideoExt(src) {
		if g.ffmpegPath == "" || g.NoVideoThumbs {
			http.NotFound(w, r)
			return true
		}
		data, err = GenerateOrLoadVideoThumb(src, g.thumbCacheDir(), g.ffmpegPath, g.thumbConfig())
	} else {
		data, err = GenerateOrLoadThumb(src, g.thumbCacheDir(), g.thumbConfig())
	}
	if err != nil {
		http.Error(w, "media_gallery: thumb generation failed: "+err.Error(), http.StatusInternalServerError)
		return true
	}
	// Per user request 2026-07-01: synchronously populate the
	// dimensions + EXIF sidecars in the same request that
	// generated the thumb. This is a change from the
	// previous (asynchronous) approach where sidecars were
	// written by the scanner's background enrichment
	// goroutine, fired on every page load. The new behavior
	// is: when a thumb is requested and we (re)generate it,
	// the meta + exif sidecars are written IN THIS REQUEST
	// before the response is sent. The next request to this
	// thumb (or the next page load that includes this file)
	// finds the sidecars already present — no race, no
	// "partial cache" state, no background goroutine needed.
	//
	// Cost analysis:
	// - Cold path (first request for a fresh thumb): the
	//   thumb itself is ~220ms (decode + resize + encode).
	//   Adding readDimensionsCached (~1-5ms image-header
	//   parse + 1ms file write) and readExifCached (~1-5ms
	//   EXIF parse + 1ms file write) adds ~4-12ms total.
	//   Negligible compared to the 220ms thumb gen.
	// - Warm path (thumb already cached): both sidecars
	//   are also already present (they were written the
	//   first time). readDimensionsCached / readExifCached
	//   do the mtime check (~3 os.Stat calls each, ~7µs
	//   total) and return immediately. Effectively free.
	//
	// The video path always uses webp; the image path uses
	// the operator-configured format (default webp).
	thumbExt := "webp"
	if !isVideoExt(src) {
		// Reuse the same extension-mapping logic as
		// GenerateOrLoadThumb: only "jpeg"/"jpg" -> "jpg" and
		// "png" -> "png"; everything else (including the
		// default "webp") -> "webp".
		if g.ThumbFormat == "jpeg" || g.ThumbFormat == "jpg" {
			thumbExt = "jpg"
		} else if g.ThumbFormat == "png" {
			thumbExt = "png"
		}
	}
	cacheDir := g.thumbCacheDir()
	// Per user request 2026-07-01: synchronously populate
	// the .meta and .exif sidecars. The mtime check in
	// each of these functions is the "is it stale?" check
	// the user asked for — if the source's mtime is newer
	// than the sidecar's, the sidecar is regenerated. This
	// means: serve the thumb if the source mtime hasn't
	// changed, the thumb exists, and the sidecars (meta +
	// exif) are not stale. Otherwise, create them.
	//
	// For images: write both .meta and .exif.
	// For videos: only .meta (videos don't have EXIF).
	// The no_exif directive disables the EXIF read.
	if isVideoExt(src) {
		// Video path: just dimensions. ffmpeg already
		// extracted the dimensions when it generated the
		// thumb, so this is a tiny sidecar write (a few
		// hundred bytes).
		_, _, _ = readDimensionsCached(src, cacheDir, thumbExt)
	} else {
		// Image path: dimensions + EXIF.
		_, _, _ = readDimensionsCached(src, cacheDir, thumbExt)
		if !g.NoExif {
			_, _ = readExifCached(src, cacheDir, thumbExt)
		}
	}
	// Per user request 2026-06-29: touch the .meta sidecar
	// to mark this thumb as "recently used". The .meta mtime
	// then acts as the LRU timestamp for cache eviction —
	// older .meta files get evicted first when the cap is
	// hit. This decouples cache eviction from the thumb's
	// own mtime (which now mirrors the source's mtime for
	// semantic correctness in the staleness check).
	touchMetaAtUse(src, cacheDir, thumbExt)
	w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", g.ThumbTTLMinutes*60)) // thumbs are immutable per source mtime
	_, _ = w.Write(data)
	return true
}

// thumbCacheDir returns the directory where thumbnail files are
// cached. Defaults to /var/cache/caddy-gallery but can be overridden
// via the GALLERY_THUMB_CACHE_DIR env var (useful for testing).
func (g *Gallery) thumbCacheDir() string {
	if d := os.Getenv("GALLERY_THUMB_CACHE_DIR"); d != "" {
		return d
	}
	return "/var/cache/caddy-gallery"
}

// thumbConfig returns the configured ThumbConfig for this gallery.
// Used by serveThumb to pass the configured width/height/format
// to GenerateOrLoadThumb. The values are set in Provision (from
// the Caddyfile directives or defaults).
func (g *Gallery) thumbConfig() ThumbConfig {
	return ThumbConfig{
		Width:             g.ThumbWidth,
		Height:            g.ThumbHeight,
		Format:            g.ThumbFormat,
		MaxCacheSizeMB:    g.MaxCacheSizeMB,
		CacheStatsTracker: g.CacheStatsTracker,
	}
}

// evictIfOver brings the thumb cache directory under the
// configured cap by deleting the oldest files (by file mtime)
// until the directory is at 80% of the cap (20% headroom
// to avoid thrashing). If maxMB <= 0, no cap is enforced —
// the cache grows unbounded (the pre-feature behavior).
//
// Per user request 2026-06-27: cap the on-disk thumb cache
// to prevent runaway growth on galleries with many images.
// Default: 1024 MB (1 GB).
//
// Eviction policy: FIFO by file mtime. The cache file names
// are sha256(source path) — opaque hex, not sorted. We use
// the file's mtime on disk (the WRITE time) to determine
// the oldest. This is "good enough" for the operator's
// concern (total size bounded, not perfect recency). For a
// true LRU, use filesystem atime or a separate LRU log.
//
// Safe to call concurrently (multiple goroutines may call
// this when many thumbs are written simultaneously). The
// underlying os operations (os.Stat, os.Remove) are atomic
// per file; worst case is one goroutine's Remove races with
// another's, resulting in ENOENT on one of them (which is
// fine — the cache will just be slightly more aggressive).
// cacheFile holds the info needed by the eviction sweep
// for one thumb in the cache. The sweep sorts by lruTime
// (oldest first) and deletes until the cache is at 80% of
// the cap.
//
// Per user request 2026-06-30: the cache uses a 2-level
// nested hash layout. The type is at package level (not
// inside evictIfOver) so the helper walkNestedCacheDir
// can return a list of cacheFile.
type cacheFile struct {
	size     int64
	lruTime  int64 // unix nanoseconds — .meta mtime if present, else thumb mtime
	path     string
	sidecars []string // .meta and .exif to delete with this thumb
}

func evictIfOver(cacheDir string, maxMB int, tracker *cacheStatsTracker) {
	if maxMB <= 0 {
		return // no cap (unbounded)
	}
	maxBytes := int64(maxMB) * 1024 * 1024
	targetBytes := maxBytes * 8 / 10 // 80% of cap (20% headroom)

	// Walk the cache directory and collect (size, lruTime, path)
	// for each cached thumb. Per user request 2026-06-29:
	// the eviction order is determined by the .meta sidecar's
	// mtime (which serveThumb touches on every access via
	// touchMetaAtUse). This gives us proper LRU eviction —
	// frequently-accessed thumbs stay in the cache,
	// rarely-accessed ones get evicted when the cap is hit.
	// The thumb's own mtime no longer drives eviction (since
	// the thumb's mtime now mirrors the source's mtime for
	// semantic correctness in the staleness check).
	//
	// Per user request 2026-06-30: the cache uses a 2-level
	// nested hash layout (e.g. <cacheDir>/<aa>/<bb>/<rest>.webp)
	// for O(1) filesystem lookups at large cache sizes. The
	// eviction sweep walks BOTH the new nested layout AND the
	// legacy flat layout (for caches populated before the
	// nested layout was introduced). Legacy files encountered
	// here are opportunistically MIGRATED to the new layout
	// (best-effort rename), so the legacy flat dir empties
	// out over time without a separate migration script.
	//
	// The total cache size is computed from the .webp/.jpg/.png
	// thumb files (not the sidecars). When we evict a thumb,
	// we also delete its .meta and .exif sidecars so they
	// don't linger in the cache (they're orphaned otherwise).
	var files []cacheFile
	var totalBytes int64
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		// Cache dir doesn't exist yet (no thumbs cached) —
		// nothing to evict. Not an error.
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() {
			// Per user request 2026-06-30: the legacy flat
			// layout is no longer supported. Legacy files
			// (if any are still in the cache after the
			// regeneration) are silently ignored by the
			// eviction sweep. They'll be cleaned up by a
			// manual cache wipe or by the next migration
			// sweep.
			continue
		}
		// Nested subdir (e.g. "ff" for the first byte
		// of the hash). Walk into it recursively.
		subFiles, subBytes, _ := walkNestedCacheDir(filepath.Join(cacheDir, name))
		files = append(files, subFiles...)
		totalBytes += subBytes
	}

	// Under cap — nothing to do
	if totalBytes <= maxBytes {
		return
	}

	// Over cap — sort by LRU time (oldest = least recently
	// used first) and delete until under the target. We
	// target 80% of the cap to leave headroom for future
	// writes (avoids evicting on every write when the cap
	// is tight).
	sort.Slice(files, func(i, j int) bool {
		return files[i].lruTime < files[j].lruTime
	})
	for _, f := range files {
		if totalBytes <= targetBytes {
			break
		}
		// Delete the thumb + any sidecars. Sidecar removal
		// errors are best-effort (the next sweep will
		// clean them up).
		_ = os.Remove(f.path)
		for _, sc := range f.sidecars {
			_ = os.Remove(sc)
		}
		totalBytes -= f.size
	}
}

// walkNestedCacheDir walks one level of the nested cache
// layout (e.g. <cacheDir>/<aa>/<bb>/*.webp). Returns the
// list of (cacheFile, size) and the total bytes. Per user
// request 2026-06-30: the 2-level hash layout means we
// walk at most 256^2 = 65,536 inner subdirs in the worst
// case, but each inner subdir typically has 0-1 files
// (because the hash is uniform). On a 24k cache, each
// inner subdir has ~0.4 files on average, so each walk
// step is O(1).
//
// Error handling: we silently ignore ReadDir errors on
// individual inner subdirs (a corrupted subdir shouldn't
// fail the entire eviction sweep). Only an error on the
// top-level subdir is reported (and we return whatever
// we managed to scan so far).
func walkNestedCacheDir(subdir string) ([]cacheFile, int64, error) {
	var out []cacheFile
	var total int64
	inner, err := os.ReadDir(subdir)
	if err != nil {
		return nil, 0, err
	}
	for _, e := range inner {
		innerPath := filepath.Join(subdir, e.Name())
		if e.IsDir() {
			// One more level: <aa>/<bb>/*.webp
			innermost, err := os.ReadDir(innerPath)
			if err != nil {
				continue
			}
			for _, ee := range innermost {
				cf, sz := scanNestedThumb(filepath.Join(innerPath, ee.Name()), ee)
				if cf != nil {
					out = append(out, *cf)
					total += sz
				}
			}
			continue
		}
		// File directly in <aa> (shouldn't happen in a
		// well-formed nested layout, but handle it).
		cf, sz := scanNestedThumb(innerPath, e)
		if cf != nil {
			out = append(out, *cf)
			total += sz
		}
	}
	return out, total, nil
}

// scanNestedThumb is a helper for walkNestedCacheDir that
// collects the cacheFile info for one nested-layout thumb.
// Returns nil if the entry is not a thumb file.
func scanNestedThumb(fullPath string, entry os.DirEntry) (*cacheFile, int64) {
	name := entry.Name()
	if !isThumbFile(name) {
		return nil, 0
	}
	info, err := entry.Info()
	if err != nil {
		return nil, 0
	}
	lruTime := info.ModTime().UnixNano()
	metaFile := fullPath + ".meta"
	if metaInfo, err := os.Stat(metaFile); err == nil {
		lruTime = metaInfo.ModTime().UnixNano()
	}
	sidecars := []string{metaFile}
	exifFile := fullPath + ".exif"
	if _, err := os.Stat(exifFile); err == nil {
		sidecars = append(sidecars, exifFile)
	}
	return &cacheFile{
		size:     info.Size(),
		lruTime:  lruTime,
		path:     fullPath,
		sidecars: sidecars,
	}, info.Size()
}

// isThumbFile reports whether the given cache file name is
// a thumb (recognised extensions: .webp, .jpg, .jpeg, .png).
// Used by the eviction logic to skip sidecar files when
// computing the cache size. Per user request 2026-06-29:
// .meta and .exif sidecars are NOT counted toward the cache
// cap (they're tiny and get deleted with their parent thumb
// during eviction).
func isThumbFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".webp", ".jpg", ".jpeg", ".png":
		return true
	}
	return false
}

// maybeEvictAsync is a fire-and-forget eviction. It runs
// evictIfOver in a goroutine so the request that triggered
// the cache write doesn't pay the eviction cost. Safe because
// the cache write has already completed (the new thumb is on
// disk); the eviction only removes OLDER files.
//
// The cap is small (single-digit MB for the walk on a 1 GB
// cap), and the per-write amortized cost is well under 1ms
// for typical galleries. Worst case: a gallery with 100,000
// thumbs where every write triggers eviction. That's a
// pathological case — the operator's gallery is too large
// for the cap. Setting a larger cap is the right fix.
func maybeEvictAsync(cacheDir string, maxMB int, tracker *cacheStatsTracker) {
	if maxMB <= 0 {
		return
	}
	go evictIfOver(cacheDir, maxMB, tracker)
}

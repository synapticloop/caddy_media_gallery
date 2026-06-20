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
	"strings"

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
}

// ThumbPath returns the on-disk path where the thumbnail for src
// should be cached. The filename is the first 16 bytes of the SHA256
// of src's absolute path, hex-encoded (32 hex chars) + ".webp".
// Using a content-hash means cache entries are stable across renames
// of the parent directory (as long as the absolute source path stays
// the same) and collisions are effectively impossible.
func ThumbPath(src, cacheDir string) string {
	abs, err := filepath.Abs(src)
	if err != nil {
		// Fall back to the raw path if Abs fails (shouldn't happen
		// in practice but avoids a panic).
		abs = src
	}
	h := sha256.Sum256([]byte(abs))
	return filepath.Join(cacheDir, hex.EncodeToString(h[:16])+".webp")
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
	if cacheInfo, err := os.Stat(cacheFile); err == nil {
		// Cache hit: source must not be newer than cache.
		if !cacheInfo.ModTime().Before(srcInfo.ModTime()) {
			return os.ReadFile(cacheFile)
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
	_ = os.WriteFile(cacheFile, buf.Bytes(), 0o644)

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
	if cacheInfo, err := os.Stat(cacheFile); err == nil {
		if !cacheInfo.ModTime().Before(srcInfo.ModTime()) {
			return os.ReadFile(cacheFile)
		}
	}
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
	return os.ReadFile(cacheFile)
}

// thumbCachePath returns the on-disk path where the thumbnail for
// src should be cached, for a specific output extension. The
// filename is the first 16 bytes of the SHA256 of src's absolute
// path, hex-encoded (32 hex chars) + "." + ext. Using a
// content-hash means cache entries are stable across renames of
// the parent directory (as long as the absolute source path stays
// the same) and collisions are effectively impossible.
func thumbCachePath(src, cacheDir, ext string) string {
	abs, err := filepath.Abs(src)
	if err != nil {
		// Fall back to the raw path if Abs fails (shouldn't
		// happen in practice but avoids a panic).
		abs = src
	}
	h := sha256.Sum256([]byte(abs))
	return filepath.Join(cacheDir, hex.EncodeToString(h[:16])+"."+ext)
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
		http.Error(w, "image_gallery: no root configured", http.StatusInternalServerError)
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
		http.Error(w, "image_gallery: thumb generation failed: "+err.Error(), http.StatusInternalServerError)
		return true
	}
	w.Header().Set("Content-Type", "image/webp")
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
		Width:  g.ThumbWidth,
		Height: g.ThumbHeight,
		Format: g.ThumbFormat,
	}
}

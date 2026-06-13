package gallery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
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
// calls. The thumbnail is at most maxWidth pixels wide; the source's
// aspect ratio is preserved. Sources already ≤ maxWidth are encoded
// without resizing.
//
// The cache file is at <cacheDir>/<sha256>.webp. If the cache file
// is older than the source (by mtime), it's regenerated.
func GenerateOrLoadThumb(src, cacheDir string, maxWidth int) ([]byte, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("create cache dir: %w", err)
	}
	cacheFile := ThumbPath(src, cacheDir)

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
	if bounds.Dx() > maxWidth {
		// Resize to maxWidth wide, preserving aspect ratio.
		scale := float64(maxWidth) / float64(bounds.Dx())
		newH := int(float64(bounds.Dy()) * scale)
		dst := image.NewRGBA(image.Rect(0, 0, maxWidth, newH))
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
		thumb = dst
	}

	// Encode to WebP. We use github.com/HugoSmits86/nativewebp, a
	// pure-Go encoder with no CGO or libwebp dependency. It's
	// lossless only (VP8L), which produces larger files than lossy
	// WebP at q=80 (~2-3x bigger) but has zero system deps. For
	// 320px gallery thumbs the size is still manageable (typically
	// 10-50KB per thumb). If size becomes a concern, swap to a
	// libwebp-backed CGO encoder — the call site stays the same.
	var buf bytes.Buffer
	if err := nativewebp.Encode(&buf, thumb, &nativewebp.Options{
		CompressionLevel: nativewebp.BestCompression,
	}); err != nil {
		return nil, fmt.Errorf("encode webp: %w", err)
	}
	// Write to cache (best-effort; a write error shouldn't fail the
	// request — the in-memory bytes are still good).
	_ = os.WriteFile(cacheFile, buf.Bytes(), 0o644)

	// Return a copy of the bytes (so callers can't mutate our buffer).
	out := make([]byte, buf.Len())
	copy(out, buf.Bytes())
	return out, nil
}

// findSourceForThumb looks up the source file for a thumb request
// given the basename (without extension). Returns the full source
// path if found, empty string if not. The lookup tries each
// image extension in turn.
func findSourceForThumb(root, basename string) string {
	for ext := range imageExtsForThumb {
		candidate := filepath.Join(root, basename+ext)
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
func (g *Gallery) serveThumb(w http.ResponseWriter, r *http.Request) bool {
	// r.URL.Path is the post-handle_path prefix-stripped path. For
	// a request to /images/_thumbs/photo.webp with route
	// "handle_path /images/* { ... }", Caddy strips /images/ so
	// r.URL.Path is "_thumbs/photo.webp". Check for the prefix
	// WITHOUT a leading slash.
	const prefix = "_thumbs/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	// rest is "photo.webp" — strip the .webp suffix to get the
	// basename. Be defensive: if it doesn't end in .webp, it's not
	// a thumb request.
	if !strings.HasSuffix(rest, ".webp") {
		http.NotFound(w, r)
		return true
	}
	basename := strings.TrimSuffix(rest, ".webp")
	// Reject path traversal attempts.
	if strings.ContainsAny(basename, "/\\") || basename == "" || basename == "." || basename == ".." {
		http.NotFound(w, r)
		return true
	}
	if g.Root == "" {
		http.Error(w, "image_gallery: no root configured", http.StatusInternalServerError)
		return true
	}
	src := findSourceForThumb(g.Root, basename)
	if src == "" {
		http.NotFound(w, r)
		return true
	}
	data, err := GenerateOrLoadThumb(src, g.thumbCacheDir(), 320)
	if err != nil {
		http.Error(w, "image_gallery: thumb generation failed: "+err.Error(), http.StatusInternalServerError)
		return true
	}
	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Cache-Control", "public, max-age=86400") // thumbs are immutable per source mtime
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

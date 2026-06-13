package gallery

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makeTestJPEG writes a 640x480 solid-color JPEG to a fresh temp file
// and returns the path. Used as a source image for thumb tests.
func makeTestJPEG(t *testing.T, w, h int) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "src.jpg")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	// Fill with a simple gradient so the bytes are non-trivial.
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8(x % 256),
				G: uint8(y % 256),
				B: 128,
				A: 255,
			})
		}
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestThumbPath_Stable(t *testing.T) {
	// Same source path → same thumb path, regardless of cache dir.
	a := ThumbPath("/var/www/html/images/foo.jpg", "/var/cache/caddy-gallery")
	b := ThumbPath("/var/www/html/images/foo.jpg", "/var/cache/caddy-gallery")
	if a != b {
		t.Errorf("ThumbPath is not stable: %q vs %q", a, b)
	}
}

func TestThumbPath_DeterministicSuffix(t *testing.T) {
	// The thumb filename is sha256-based. Verify the suffix is 32 hex
	// chars + .webp so URLs are predictable.
	p := ThumbPath("/var/www/html/images/foo.jpg", "/var/cache/caddy-gallery")
	base := filepath.Base(p)
	if !strings.HasSuffix(base, ".webp") {
		t.Errorf("thumb path %q should end in .webp", p)
	}
	stem := strings.TrimSuffix(base, ".webp")
	if len(stem) != 32 {
		t.Errorf("thumb stem %q should be 32 hex chars (16-byte sha256), got len %d", stem, len(stem))
	}
	for _, c := range stem {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("thumb stem %q contains non-hex char %q", stem, c)
			break
		}
	}
}

func TestThumbPath_DifferentInputsDifferentOutput(t *testing.T) {
	a := ThumbPath("/a/foo.jpg", "/cache")
	b := ThumbPath("/b/foo.jpg", "/cache")
	if a == b {
		t.Error("different source paths should give different thumb paths")
	}
}

func TestGenerateOrLoadThumb_CreatesWebP(t *testing.T) {
	src := makeTestJPEG(t, 640, 480)
	cache := t.TempDir()
	data, err := GenerateOrLoadThumb(src, cache, 320)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 100 {
		t.Errorf("thumb data too small: %d bytes", len(data))
	}
	// Verify it's a WebP by magic bytes: "RIFF....WEBP"
	if !bytes.HasPrefix(data, []byte("RIFF")) {
		t.Errorf("thumb does not start with RIFF magic, got %q", data[:8])
	}
	if len(data) < 12 || string(data[8:12]) != "WEBP" {
		t.Errorf("thumb missing WEBP marker at offset 8, got %q", data[8:12])
	}
}

func TestGenerateOrLoadThumb_CachesResult(t *testing.T) {
	src := makeTestJPEG(t, 640, 480)
	cache := t.TempDir()
	first, err := GenerateOrLoadThumb(src, cache, 320)
	if err != nil {
		t.Fatal(err)
	}
	// Second call should return identical bytes from cache.
	second, err := GenerateOrLoadThumb(src, cache, 320)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Error("cached thumb differs from first generation")
	}
	// Verify the cache file exists on disk.
	cacheFile := ThumbPath(src, cache)
	if _, err := os.Stat(cacheFile); err != nil {
		t.Errorf("expected cache file at %q, got error: %v", cacheFile, err)
	}
}

func TestGenerateOrLoadThumb_RegeneratesOnSourceMtimeChange(t *testing.T) {
	src := makeTestJPEG(t, 640, 480)
	cache := t.TempDir()
	_, err := GenerateOrLoadThumb(src, cache, 320)
	if err != nil {
		t.Fatal(err)
	}
	// Record the cache file's mtime before the source change.
	cacheFile := ThumbPath(src, cache)
	firstInfo, err := os.Stat(cacheFile)
	if err != nil {
		t.Fatal(err)
	}

	// Bump source mtime into the future (don't touch the contents —
	// we want a clean regeneration that produces a valid WebP).
	// Add 2 seconds to ensure the new mtime is strictly after the
	// cache's mtime regardless of filesystem timestamp resolution.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(src, future, future); err != nil {
		t.Fatal(err)
	}

	// Second call should detect the source is newer than the cache
	// and regenerate. We then check the cache file's mtime
	// advanced.
	data, err := GenerateOrLoadThumb(src, cache, 320)
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(cacheFile)
	if err != nil {
		t.Fatal(err)
	}
	if !secondInfo.ModTime().After(firstInfo.ModTime()) {
		t.Errorf("expected cache mtime to advance after regeneration: first=%v second=%v",
			firstInfo.ModTime(), secondInfo.ModTime())
	}
	// Sanity: the regenerated thumb is still valid WebP.
	if !bytes.HasPrefix(data, []byte("RIFF")) {
		t.Error("regenerated thumb is not valid RIFF/WebP")
	}
}

func TestGenerateOrLoadThumb_SmallSourcePassesThrough(t *testing.T) {
	// Source is 200x150 — already smaller than 320 wide. Should be
	// encoded as WebP without resizing.
	src := makeTestJPEG(t, 200, 150)
	cache := t.TempDir()
	data, err := GenerateOrLoadThumb(src, cache, 320)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte("RIFF")) {
		t.Error("small source: thumb is not valid RIFF/WebP")
	}
}

func TestGenerateOrLoadThumb_BadSourceReturnsError(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "corrupt.jpg")
	if err := os.WriteFile(src, []byte("not actually a jpeg"), 0644); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	if _, err := GenerateOrLoadThumb(src, cache, 320); err == nil {
		t.Error("expected error for corrupt source, got nil")
	}
}

func TestGenerateOrLoadThumb_MissingSourceReturnsError(t *testing.T) {
	cache := t.TempDir()
	if _, err := GenerateOrLoadThumb("/this/does/not/exist.jpg", cache, 320); err == nil {
		t.Error("expected error for missing source, got nil")
	}
}

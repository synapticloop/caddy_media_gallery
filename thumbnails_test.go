package gallery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http/httptest"
	"os"
	"os/exec"
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

// TestThumbPath_DeterministicSuffix verifies the new
// 2-level nested cache layout: the thumb is at
// <cacheDir>/<aa>/<bb>/<rest>.webp where:
//   - <aa> is the first 2 hex chars of the sha256 (a subdir)
//   - <bb> is the next 2 hex chars (another subdir)
//   - <rest> is the remaining 28 hex chars (the basename)
//
// Total filename: 28 hex chars + ".webp" (4 chars) = 32 chars
// but split across 2 subdirs. We verify the path structure:
// - 2 path segments of exactly 2 hex chars each (the subdirs)
// - 1 filename of exactly 28 hex chars + .webp
func TestThumbPath_DeterministicSuffix(t *testing.T) {
	p := ThumbPath("/var/www/html/images/foo.jpg", "/var/cache/caddy-gallery")
	rel, err := filepath.Rel("/var/cache/caddy-gallery", p)
	if err != nil {
		t.Fatalf("ThumbPath %q is not under cacheDir: %v", p, err)
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 3 {
		t.Fatalf("thumb path %q should have 3 components under cacheDir (aa/bb/rest.webp), got %d: %v", p, len(parts), parts)
	}
	subdir1, subdir2, filename := parts[0], parts[1], parts[2]
	// Subdirs: 2 hex chars each
	for i, sd := range []string{subdir1, subdir2} {
		if len(sd) != 2 {
			t.Errorf("subdir %d (%q) should be 2 hex chars, got len %d", i, sd, len(sd))
		}
		for _, c := range sd {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Errorf("subdir %d %q contains non-hex char %q", i, sd, c)
				break
			}
		}
	}
	// Filename: 28 hex chars + .webp
	if !strings.HasSuffix(filename, ".webp") {
		t.Errorf("filename %q should end in .webp", filename)
	}
	stem := strings.TrimSuffix(filename, ".webp")
	if len(stem) != 28 {
		t.Errorf("filename stem %q should be 28 hex chars (rest of sha256), got len %d", stem, len(stem))
	}
	for _, c := range stem {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("filename stem %q contains non-hex char %q", stem, c)
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
	data, err := GenerateOrLoadThumb(src, cache, ThumbConfig{Width: 320, Height: 320, Format: "webp"})
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
	first, err := GenerateOrLoadThumb(src, cache, ThumbConfig{Width: 320, Height: 320, Format: "webp"})
	if err != nil {
		t.Fatal(err)
	}
	// Second call should return identical bytes from cache.
	second, err := GenerateOrLoadThumb(src, cache, ThumbConfig{Width: 320, Height: 320, Format: "webp"})
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
	_, err := GenerateOrLoadThumb(src, cache, ThumbConfig{Width: 320, Height: 320, Format: "webp"})
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
	data, err := GenerateOrLoadThumb(src, cache, ThumbConfig{Width: 320, Height: 320, Format: "webp"})
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
	data, err := GenerateOrLoadThumb(src, cache, ThumbConfig{Width: 320, Height: 320, Format: "webp"})
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
	if _, err := GenerateOrLoadThumb(src, cache, ThumbConfig{Width: 320, Height: 320, Format: "webp"}); err == nil {
		t.Error("expected error for corrupt source, got nil")
	}
}

func TestGenerateOrLoadThumb_MissingSourceReturnsError(t *testing.T) {
	cache := t.TempDir()
	if _, err := GenerateOrLoadThumb("/this/does/not/exist.jpg", cache, ThumbConfig{Width: 320, Height: 320, Format: "webp"}); err == nil {
		t.Error("expected error for missing source, got nil")
	}
}

// TestGenerateOrLoadThumb_FormatDispatch verifies the new
// ThumbConfig format dispatch: jpeg, png, and webp all work, and
// the cache filename uses the right extension.
func TestGenerateOrLoadThumb_FormatDispatch(t *testing.T) {
	cases := []struct {
		format string
		ext    string
		magic  []byte
	}{
		{"jpeg", "jpg", []byte{0xFF, 0xD8, 0xFF}}, // JPEG SOI marker
		{"jpg", "jpg", []byte{0xFF, 0xD8, 0xFF}},
		{"png", "png", []byte{0x89, 0x50, 0x4E, 0x47}}, // PNG magic
		{"webp", "webp", []byte{'R', 'I', 'F', 'F'}},   // RIFF (WebP container)
	}
	for _, c := range cases {
		t.Run(c.format, func(t *testing.T) {
			src := writeTestPNG(t, t.TempDir())
			cache := t.TempDir()
			data, err := GenerateOrLoadThumb(src, cache, ThumbConfig{
				Width: 100, Height: 100, Format: c.format,
			})
			if err != nil {
				t.Fatalf("GenerateOrLoadThumb(%q): %v", c.format, err)
			}
			if len(data) < len(c.magic) {
				t.Fatalf("output too short: %d bytes", len(data))
			}
			for i, b := range c.magic {
				if data[i] != b {
					t.Errorf("magic byte %d: got 0x%02X, want 0x%02X", i, data[i], b)
				}
			}
			// Verify cache file has the right extension
			// (now in the 2-level nested layout).
			abs, _ := filepath.Abs(src)
			h := sha256.Sum256([]byte(abs))
			hexHash := hex.EncodeToString(h[:16])
			subdir1 := hexHash[:2]
			subdir2 := hexHash[2:4]
			rest := hexHash[4:]
			expectedPath := filepath.Join(cache, subdir1, subdir2, rest+"."+c.ext)
			if _, err := os.Stat(expectedPath); err != nil {
				t.Errorf("expected cache file at %q, got err: %v", expectedPath, err)
			}
		})
	}
}

// TestGenerateOrLoadThumb_UnsupportedFormatRejected verifies that
// requesting a format we don't support returns a clear error (not
// a 500 from a panic or empty bytes).
func TestGenerateOrLoadThumb_UnsupportedFormatRejected(t *testing.T) {
	src := writeTestPNG(t, t.TempDir())
	cache := t.TempDir()
	_, err := GenerateOrLoadThumb(src, cache, ThumbConfig{
		Width: 100, Height: 100, Format: "avif",
	})
	if err == nil {
		t.Fatal("expected error for unsupported format avif, got nil")
	}
	if !strings.Contains(err.Error(), "avif") {
		t.Errorf("expected error to mention avif, got: %v", err)
	}
}

// TestGenerateOrLoadThumb_DefaultFormatWhenEmpty verifies that
// passing Format="" falls back to webp (the default).
func TestGenerateOrLoadThumb_DefaultFormatWhenEmpty(t *testing.T) {
	src := writeTestPNG(t, t.TempDir())
	cache := t.TempDir()
	data, err := GenerateOrLoadThumb(src, cache, ThumbConfig{
		Width: 100, Height: 0, Format: "", // empty Format + 0 Height
	})
	if err != nil {
		t.Fatalf("GenerateOrLoadThumb with empty Format: %v", err)
	}
	if len(data) < 4 || string(data[:4]) != "RIFF" {
		t.Errorf("expected WebP (RIFF) output when Format is empty, got first 4 bytes: %q", data[:4])
	}
}

// writeTestPNG writes a valid 1x1 PNG to disk using Go's stdlib
// image/png encoder, and returns the path. Used by the format-
// dispatch test to ensure we have a decodable source in any of
// the supported formats (jpeg/png/webp all accept PNG input).
func writeTestPNG(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "test.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// 1x1 black RGBA pixel.
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestIsVideoExt verifies that isVideoExt correctly classifies
// common video container extensions and ignores images /
// non-media. Per Phase 62 — supports the new video-thumb
// dispatch logic in serveThumb.
func TestIsVideoExt(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Common video containers we generate thumbnails for.
		{"clip.mp4", true},
		{"movie.m4v", true},
		{"anim.webm", true},
		{"raw.mov", true},
		{"episode.mkv", true},
		{"old.avi", true},
		{"vp9.webm", true},
		{"theora.ogv", true},
		// Case-insensitive (strings.ToLower is applied).
		{"CLIP.MP4", true},
		{"Photo.MOV", true},
		// Images — should NOT be classified as video.
		{"photo.jpg", false},
		{"photo.png", false},
		{"anim.gif", false},
		{"art.svg", false},
		{"icon.avif", false},
		// Other files — also not video.
		{"readme.txt", false},
		{"archive.zip", false},
		// No extension — not a video.
		{"README", false},
	}
	for _, tc := range cases {
		if got := isVideoExt(tc.path); got != tc.want {
			t.Errorf("isVideoExt(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestGenerateOrLoadVideoThumb_EmptyFFmpegPathReturnsError covers
// the defensive guard: if the caller passes an empty ffmpegPath,
// the function should fail cleanly rather than exec.Command("")
// (which on some platforms returns a "no such file" error but
// is confusing — we want a clear message).
func TestGenerateOrLoadVideoThumb_EmptyFFmpegPathReturnsError(t *testing.T) {
	_, err := GenerateOrLoadVideoThumb("/some/video.mp4", t.TempDir(), "", ThumbConfig{Width: 320, Height: 320, Format: "webp"})
	if err == nil {
		t.Fatal("expected error when ffmpegPath is empty, got nil")
	}
	if !strings.Contains(err.Error(), "ffmpeg") {
		t.Errorf("error message should mention ffmpeg, got: %v", err)
	}
}

// TestGenerateOrLoadVideoThumb_MissingSourceReturnsError covers
// the stat-the-source guard. Mirrors the existing
// TestGenerateOrLoadThumb_MissingSourceReturnsError.
func TestGenerateOrLoadVideoThumb_MissingSourceReturnsError(t *testing.T) {
	_, err := GenerateOrLoadVideoThumb("/nonexistent/video.mp4", t.TempDir(), "/usr/bin/ffmpeg", ThumbConfig{Width: 320, Height: 320, Format: "webp"})
	if err == nil {
		t.Fatal("expected error for missing source file, got nil")
	}
	if !strings.Contains(err.Error(), "stat") {
		t.Errorf("error should mention stat failure, got: %v", err)
	}
}

// TestGenerateOrLoadVideoThumb_EndToEndWithRealFFmpeg is gated on
// ffmpeg being available on the test host. If ffmpeg is not
// installed (or isn't in PATH), the test is skipped — the
// production code still handles the "ffmpeg missing" case via
// serveThumb's 404 fallback.
//
// To exercise the real code path on a host with ffmpeg, this
// test:
//  1. Synthesizes a 1-second test video with ffmpeg itself
//     (using the lavfi virtual input — no need for a fixture file)
//  2. Calls GenerateOrLoadVideoThumb on it
//  3. Verifies the cache file is created
//  4. Verifies the cache file is a valid webp image (by checking
//     the magic bytes: "RIFF????WEBP")
func TestGenerateOrLoadVideoThumb_EndToEndWithRealFFmpeg(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available on this host; skipping end-to-end test")
	}
	tmpDir := t.TempDir()
	srcPath := filepath.Join(tmpDir, "testsrc.mp4")
	cacheDir := filepath.Join(tmpDir, "cache")

	// Synthesize a 1-second test video: 320x240, solid red,
	// h264 + aac, mp4 container. The lavfi virtual input
	// (color=red:size=320x240:duration=1:rate=30) avoids
	// needing a real fixture file.
	mkCmd := exec.Command(ffmpegPath,
		"-y",
		"-f", "lavfi",
		"-i", "color=red:size=320x240:duration=1:rate=30",
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac",
		srcPath,
	)
	if mkOut, err := mkCmd.CombinedOutput(); err != nil {
		t.Skipf("could not synthesize test video with ffmpeg: %v (%s)", err, mkOut)
	}

	// Now call GenerateOrLoadVideoThumb.
	cfg := ThumbConfig{Width: 320, Height: 320, Format: "webp"}
	data, err := GenerateOrLoadVideoThumb(srcPath, cacheDir, ffmpegPath, cfg)
	if err != nil {
		t.Fatalf("GenerateOrLoadVideoThumb failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("returned empty thumb bytes")
	}
	// Verify webp magic bytes: "RIFF????WEBP"
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		t.Errorf("output is not a valid webp file; first 12 bytes: % x", data[:min(12, len(data))])
	}
	// Verify cache file was written.
	expectedCacheFile := thumbCachePath(srcPath, cacheDir, "webp")
	if _, err := os.Stat(expectedCacheFile); err != nil {
		t.Errorf("cache file not created at %s: %v", expectedCacheFile, err)
	}
	// Second call should be a cache hit (no ffmpeg invocation).
	// We can't directly detect "no ffmpeg invocation" but we can
	// verify the result is identical (deterministic).
	data2, err := GenerateOrLoadVideoThumb(srcPath, cacheDir, ffmpegPath, cfg)
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if !bytes.Equal(data, data2) {
		t.Error("second call returned different bytes (cache hit should be deterministic)")
	}
}


// TestServeThumb_CreatesSidecarsSynchronously verifies that the
// synchronous sidecar creation (added per user request 2026-07-01)
// works: a single serveThumb call leaves thumb + .meta + .exif on
// disk. Before this change, the .meta and .exif sidecars were
// created asynchronously by the scanner's background enrichment
// goroutine, fired on page loads. The new behavior is: each
// serveThumb call leaves a complete cache state for the source
// file, so the next page load / lightbox open finds the data
// already present.
func TestServeThumb_CreatesSidecarsSynchronously(t *testing.T) {
	src := makeTestJPEG(t, 640, 480)
	srcInfo, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	t.Setenv("GALLERY_THUMB_CACHE_DIR", cacheDir)
	g := &Gallery{
		ThumbWidth:   320,
		ThumbHeight:  320,
		ThumbFormat:  "webp",
		NoExif:       false,
		ThumbTTLMinutes: 1440,
	}
	// Construct a request that the handler will recognise as a
	// thumb request. The URL is /<subdir>/_thumbs/<basename>.webp
	// and the handler strips the .webp suffix to get the source
	// basename. The "subdir" path is also stripped, so the
	// source must be at root + subdir + basename.
	// The test JPEG was created at src (a temp dir); we mirror
	// that as the gallery root and use a subdir of "".
	// The serveThumb handler expects a URL ending in .webp —
	// it strips the .webp suffix to recover the source basename.
	// The source is at filepath.Dir(src) + "src.jpg" so the
	// source basename (no extension) is "src" and the thumb URL
	// is /_thumbs/src.webp.
	req := httptest.NewRequest("GET", "/_thumbs/src.webp", nil)
	rec := httptest.NewRecorder()
	handled := g.serveThumb(rec, req, filepath.Dir(src), "_thumbs/src.webp")
	if !handled {
		t.Fatalf("serveThumb returned false (request not handled)")
	}
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// Verify the thumb was written.
	thumbPath := thumbCachePath(src, cacheDir, "webp")
	if _, err := os.Stat(thumbPath); err != nil {
		t.Errorf("thumb not created: %v", err)
	}
	// Verify the .meta sidecar was written by the synchronous
	// readDimensionsCached call.
	metaPath := thumbCachePath(src, cacheDir, "webp") + ".meta"
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		t.Errorf(".meta sidecar not created: %v", err)
	}
	// The .meta should contain the source dimensions (640 480),
	// not the thumb dimensions (320x scaled). Verify.
	var w, h int
	if _, err := fmt.Sscanf(string(metaData), "%d %d", &w, &h); err != nil {
		t.Errorf(".meta content not parseable as 'W H': %q (err: %v)", metaData, err)
	}
	if w != 640 || h != 480 {
		t.Errorf(".meta has %dx%d, want 640x480 (source dimensions, not thumb dimensions)", w, h)
	}
	// Verify the .exif sidecar was written by the synchronous
	// readExifCached call. The test JPEG has no EXIF, so the
	// sidecar should be "has=false" (which is the documented
	// "no EXIF" cache format).
	exifPath := thumbCachePath(src, cacheDir, "webp") + ".exif"
	exifData, err := os.ReadFile(exifPath)
	if err != nil {
		t.Errorf(".exif sidecar not created: %v", err)
	}
	if !bytes.HasPrefix(exifData, []byte("has=false")) {
		t.Errorf(".exif should be 'has=false' for a JPEG with no EXIF, got %q", exifData)
	}
	// Verify the .meta mtime was touched to time.Now() by
	// touchMetaAtUse (the LRU marker). It should be at or
	// after srcInfo.ModTime() (the .meta is written with
	// source mtime, then touched to now).
	metaInfo, err := os.Stat(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if metaInfo.ModTime().Before(srcInfo.ModTime()) {
		t.Errorf(".meta mtime %v is before source mtime %v", metaInfo.ModTime(), srcInfo.ModTime())
	}
}

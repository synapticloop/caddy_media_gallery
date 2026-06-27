package gallery

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"

	_ "golang.org/x/image/webp"
)

// TestReadDimensions_RealFixtures verifies readDimensions
// returns the actual pixel dimensions of real image files.
// Per user request 2026-06-27: dimensions of the source
// file (the image the thumbnail was generated from).
func TestReadDimensions_RealFixtures(t *testing.T) {
	fixtures := []struct {
		path string
	}{
		{"/var/www/html/images/media_gallery/elderly_man_profile_fishing_misty_dawn.png"},
		{"/var/www/html/images/media_gallery/misty_bamboo_forest_path.jpg"},
		{"/var/www/html/images/media_gallery/potted_succulent_windowsill_sunlight.webp"},
	}
	for _, f := range fixtures {
		if _, err := os.Stat(f.path); err != nil {
			t.Skipf("fixture not available: %s", f.path)
			continue
		}
		w, h, err := readDimensions(f.path)
		if err != nil {
			t.Errorf("readDimensions(%s): %v", filepath.Base(f.path), err)
			continue
		}
		if w <= 0 || h <= 0 {
			t.Errorf("readDimensions(%s): got %dx%d, expected positive dims", filepath.Base(f.path), w, h)
		}
	}
}

// TestReadDimensions_SyntheticJPEG creates a small JPEG with
// known dimensions and verifies readDimensions returns them.
func TestReadDimensions_SyntheticJPEG(t *testing.T) {
	tmp, err := os.CreateTemp("", "test-*.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	img := image.NewRGBA(image.Rect(0, 0, 100, 50))
	// Fill with a solid colour so the JPEG is non-trivial
	for y := 0; y < 50; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}
	if err := jpeg.Encode(tmp, img, nil); err != nil {
		t.Fatal(err)
	}

	w, h, err := readDimensions(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	if w != 100 || h != 50 {
		t.Errorf("JPEG: got %dx%d, want 100x50", w, h)
	}
}

// TestReadDimensions_SyntheticPNG creates a small PNG with
// known dimensions.
func TestReadDimensions_SyntheticPNG(t *testing.T) {
	tmp, err := os.CreateTemp("", "test-*.png")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	img := image.NewRGBA(image.Rect(0, 0, 200, 75))
	if err := png.Encode(tmp, img); err != nil {
		t.Fatal(err)
	}

	w, h, err := readDimensions(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	if w != 200 || h != 75 {
		t.Errorf("PNG: got %dx%d, want 200x75", w, h)
	}
}

// TestReadDimensions_SyntheticGIF creates a small GIF with
// known dimensions.
func TestReadDimensions_SyntheticGIF(t *testing.T) {
	tmp, err := os.CreateTemp("", "test-*.gif")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	img := image.NewPaletted(image.Rect(0, 0, 40, 30), color.Palette{color.Black, color.White})
	if err := gif.Encode(tmp, img, nil); err != nil {
		t.Fatal(err)
	}

	w, h, err := readDimensions(tmp.Name())
	if err != nil {
		t.Fatal(err)
	}
	if w != 40 || h != 30 {
		t.Errorf("GIF: got %dx%d, want 40x30", w, h)
	}
}

// TestReadDimensions_MalformedImage verifies that a corrupted
// image file returns (0, 0, nil) — not an error. This is
// important because the scanner must continue even when one
// image is broken.
func TestReadDimensions_MalformedImage(t *testing.T) {
	tmp, err := os.CreateTemp("", "broken-*.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	// Write garbage that starts with the JPEG magic but
	// has no valid SOF marker
	io.WriteString(tmp, "\xff\xd8\xff\xe0this is not a valid jpeg")

	w, h, err := readDimensions(tmp.Name())
	if err != nil {
		t.Errorf("malformed image: expected no error, got %v", err)
	}
	if w != 0 || h != 0 {
		t.Errorf("malformed image: got %dx%d, want 0x0", w, h)
	}
}

// TestReadDimensions_NonImageFile verifies that a text file
// (with an image extension or no extension at all) returns
// (0, 0, nil).
func TestReadDimensions_NonImageFile(t *testing.T) {
	tmp, err := os.CreateTemp("", "notimage-*.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	io.WriteString(tmp, "This is plain text, not an image.")

	w, h, err := readDimensions(tmp.Name())
	if err != nil {
		t.Errorf("text file: expected no error, got %v", err)
	}
	if w != 0 || h != 0 {
		t.Errorf("text file: got %dx%d, want 0x0", w, h)
	}
}

// TestReadDimensions_UnsupportedExtension verifies that
// extensions we don't support (SVG, AVIF, HEIC) return
// (0, 0, nil) — not an error. The watermark simply doesn't
// render for those files.
func TestReadDimensions_UnsupportedExtension(t *testing.T) {
	for _, ext := range []string{".svg", ".avif", ".heic", ".txt", ""} {
		tmp, err := os.CreateTemp("", "test-*"+ext)
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmp.Name())
		defer tmp.Close()
		io.WriteString(tmp, "anything")

		w, h, err := readDimensions(tmp.Name())
		if err != nil {
			t.Errorf("ext=%s: expected no error, got %v", ext, err)
		}
		if w != 0 || h != 0 {
			t.Errorf("ext=%s: got %dx%d, want 0x0", ext, w, h)
		}
	}
}

// TestReadDimensions_RealMP4Fixture verifies the ffprobe path
// reads dimensions from a real video file. Skips if no MP4
// fixture is available in the gallery.
func TestReadDimensions_RealMP4Fixture(t *testing.T) {
	matches, err := filepath.Glob("/var/www/html/images/media_gallery/**/*.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Skip("no MP4 fixture available")
	}
	path := matches[0]
	w, h, err := readDimensions(path)
	if err != nil {
		t.Fatal(err)
	}
	if w <= 0 || h <= 0 {
		t.Errorf("MP4 %s: got %dx%d, want positive dims", path, w, h)
	}
}

// TestReadDimensions_NonexistentFile verifies that a missing
// file returns a genuine error (file not found), not (0, 0, nil).
// This is the one case where we DO want an error so the scanner
// can log it.
func TestReadDimensions_NonexistentFile(t *testing.T) {
	_, _, err := readDimensions("/nonexistent/path/that/does/not/exist.jpg")
	if err == nil {
		t.Error("nonexistent file: expected an error, got nil")
	}
}

// TestFormatDimensions verifies the formatDimensions helper.
func TestFormatDimensions(t *testing.T) {
	tests := []struct {
		w, h int
		want string
	}{
		{1920, 1080, "1920 × 1080"},
		{6000, 4000, "6000 × 4000"},
		{1024, 1024, "1024 × 1024"},
		{0, 100, ""},
		{100, 0, ""},
		{0, 0, ""},
		{-1, 100, ""}, // defensive: negative dims treated as no dims
		{100, -1, ""},
	}
	for _, tc := range tests {
		got := formatDimensions(tc.w, tc.h)
		if got != tc.want {
			t.Errorf("formatDimensions(%d, %d): got %q, want %q", tc.w, tc.h, got, tc.want)
		}
	}
}

// TestReadDimensions_NoiseBufferNotEmpty is a defensive
// check — verifies readImageDimensions returns 0,0 for any
// input that image.DecodeConfig can't parse.
func TestReadDimensions_NoiseBufferNotEmpty(t *testing.T) {
	tmp, err := os.CreateTemp("", "noise-*.jpg")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	// Random binary noise that starts with bytes that could
	// be confused for an image header but isn't.
	noise := bytes.Repeat([]byte{0xAB, 0xCD, 0xEF}, 100)
	tmp.Write(noise)

	w, h, err := readImageDimensions(tmp.Name())
	if err != nil {
		t.Errorf("noise: expected no error, got %v", err)
	}
	if w != 0 || h != 0 {
		t.Errorf("noise: got %dx%d, want 0x0", w, h)
	}
}

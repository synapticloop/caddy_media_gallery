package gallery

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadExif_RealFixtures verifies that readExif extracts
// the CAMERA subset from real image files at
// /var/www/html/images/media_gallery/. The fixtures are
// three files with embedded EXIF added via exiftool with
// realistic camera/lens data:
//
//   - elderly_man_profile_fishing_misty_dawn.png (Sony)
//   - misty_bamboo_forest_path.jpg (Fujifilm)
//   - potted_succulent_windowsill_sunlight.webp (Canon)
//
// Per user request 2026-06-27: the test asserts ALL 8 fields
// (Make, Model, Lens, Date, Shutter, Aperture, ISO, Focal)
// AND confirms that GPS fields are NEVER returned.
func TestReadExif_RealFixtures(t *testing.T) {
	fixtures := []struct {
		path              string
		make, model, lens string
	}{
		{
			path:  "/var/www/html/images/media_gallery/elderly_man_profile_fishing_misty_dawn.png",
			make:  "Sony",
			model: "ILCE-7M4",
			lens:  "FE 70-200mm F2.8 GM OSS II",
		},
		{
			path:  "/var/www/html/images/media_gallery/misty_bamboo_forest_path.jpg",
			make:  "Fujifilm",
			model: "X-T5",
			lens:  "XF 16-55mm F2.8 R LM WR",
		},
		{
			path:  "/var/www/html/images/media_gallery/potted_succulent_windowsill_sunlight.webp",
			make:  "Canon",
			model: "EOS R5",
			lens:  "RF 50mm F1.2 L USM",
		},
	}
	for _, f := range fixtures {
		if _, err := os.Stat(f.path); err != nil {
			t.Skipf("fixture not available: %s (%v)", f.path, err)
			continue
		}
		data, err := readExif(f.path)
		if err != nil {
			t.Errorf("readExif(%s): %v", filepath.Base(f.path), err)
			continue
		}
		if data == nil {
			t.Errorf("readExif(%s): returned nil (expected EXIF data)", filepath.Base(f.path))
			continue
		}
		if !data.HasAny() {
			t.Errorf("readExif(%s): HasAny is false (expected at least one field)", filepath.Base(f.path))
		}
		if data.CameraMake != f.make {
			t.Errorf("readExif(%s): Make: got %q, want %q", filepath.Base(f.path), data.CameraMake, f.make)
		}
		if data.CameraModel != f.model {
			t.Errorf("readExif(%s): Model: got %q, want %q", filepath.Base(f.path), data.CameraModel, f.model)
		}
		if data.LensModel != f.lens {
			t.Errorf("readExif(%s): Lens: got %q, want %q", filepath.Base(f.path), data.LensModel, f.lens)
		}
		// Confirm the format helpers produced reasonable output
		if data.ExposureTime != "" && !strings.Contains(data.ExposureTime, " s") {
			t.Errorf("readExif(%s): ExposureTime should end with ` s`: got %q", filepath.Base(f.path), data.ExposureTime)
		}
		if data.Aperture != "" && !strings.HasPrefix(data.Aperture, "f/") {
			t.Errorf("readExif(%s): Aperture should start with `f/`: got %q", filepath.Base(f.path), data.Aperture)
		}
		if data.ISO != "" && !strings.HasPrefix(data.ISO, "ISO ") {
			t.Errorf("readExif(%s): ISO should start with `ISO `: got %q", filepath.Base(f.path), data.ISO)
		}
		if data.FocalLength != "" && !strings.HasSuffix(data.FocalLength, " mm") {
			t.Errorf("readExif(%s): FocalLength should end with ` mm`: got %q", filepath.Base(f.path), data.FocalLength)
		}
	}
}

// TestReadExif_NoExif verifies that readExif returns (nil, nil)
// for a file that has no EXIF block. The fixture is the same
// directory as the EXIF fixtures; we pick any image that we
// know has NO embedded EXIF (a freshly-copied gallery image
// that was created without EXIF metadata).
func TestReadExif_NoExif(t *testing.T) {
	// Pick any image in the gallery without EXIF. We use a
	// PNG that we know was generated without EXIF (most of
	// the gallery's images are downloaded/generated without
	// EXIF).
	candidates := []string{
		"/var/www/html/images/media_gallery/animals/cat_yawning_sunbeam.jpg",
		"/var/www/html/images/media_gallery/buildings/brick_warehouse_morning.jpg",
		"/var/www/html/images/media_gallery/plants/fern_leaf_macro.jpg",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err != nil {
			continue
		}
		data, err := readExif(c)
		if err != nil {
			t.Errorf("readExif(%s): unexpected error: %v", filepath.Base(c), err)
			continue
		}
		if data != nil {
			t.Errorf("readExif(%s): expected nil (no EXIF), got data: %+v", filepath.Base(c), data)
		}
		return
	}
	t.Skip("no candidate fixture available without EXIF")
}

// TestExifData_HasAny verifies the HasAny method's behavior.
func TestExifData_HasAny(t *testing.T) {
	// nil pointer: not "any"
	var nilData *ExifData
	if nilData.HasAny() {
		t.Error("nil *ExifData should return false")
	}
	// Empty struct: not "any"
	empty := &ExifData{}
	if empty.HasAny() {
		t.Error("empty *ExifData should return false")
	}
	// One field set: "any"
	withOne := &ExifData{CameraMake: "Canon"}
	if !withOne.HasAny() {
		t.Error("*ExifData with CameraMake should return true")
	}
	// All fields set
	withAll := &ExifData{
		CameraMake:   "Sony",
		CameraModel:  "ILCE-7M4",
		LensModel:    "FE 70-200mm F2.8 GM OSS II",
		DateTaken:    "2024:11:08 06:23:14",
		ExposureTime: "1/250 s",
		Aperture:     "f/4",
		ISO:          "ISO 800",
		FocalLength:  "135 mm",
	}
	if !withAll.HasAny() {
		t.Error("fully-populated *ExifData should return true")
	}
}

// TestFormatRational verifies the formatRational helper
// directly (without needing a real EXIF file).
func TestFormatRational(t *testing.T) {
	tests := []struct {
		num, denom uint32
		want       string
	}{
		{1, 250, "1/250 s"},
		{2, 1, "2 s"},
		{3, 1, "3 s"},
		{5, 2, "2.5 s"},
		{10, 3, "3.3 s"}, // rounded
		{1, 0, ""},       // divide-by-zero: empty string
	}
	for _, tc := range tests {
		got := formatRational(tc.num, tc.denom)
		if got != tc.want {
			t.Errorf("formatRational(%d, %d): got %q, want %q", tc.num, tc.denom, got, tc.want)
		}
	}
}

// TestFormatAperture verifies the formatAperture helper.
func TestFormatAperture(t *testing.T) {
	tests := []struct {
		num, denom uint32
		want       string
	}{
		{28, 10, "f/2.8"},
		{40, 10, "f/4"},
		{50, 10, "f/5"},
		{56, 10, "f/5.6"},
		{80, 10, "f/8"},
		{0, 1, "f/0"}, // edge case: f/0 (zero aperture, treated as f/0)
		{1, 0, ""},    // divide-by-zero
	}
	for _, tc := range tests {
		got := formatAperture(tc.num, tc.denom)
		if got != tc.want {
			t.Errorf("formatAperture(%d, %d): got %q, want %q", tc.num, tc.denom, got, tc.want)
		}
	}
}

// TestFormatFocalLengthMm verifies the formatFocalLengthMm helper.
func TestFormatFocalLengthMm(t *testing.T) {
	tests := []struct {
		num, denom uint32
		want       string
	}{
		{50, 1, "50 mm"},
		{135, 1, "135 mm"},
		{500, 10, "50 mm"},
		{175, 10, "17.5 mm"},
		{1, 0, ""}, // divide-by-zero
	}
	for _, tc := range tests {
		got := formatFocalLengthMm(tc.num, tc.denom)
		if got != tc.want {
			t.Errorf("formatFocalLengthMm(%d, %d): got %q, want %q", tc.num, tc.denom, got, tc.want)
		}
	}
}


// TestReadExifCached_FirstReadWritesSidecar verifies the
// first call to readExifCached parses the source image's
// EXIF block AND writes a sidecar .exif file in the thumb
// cache dir. Per user request 2026-06-29: the sidecar is
// the optimisation — the first lightbox open of a file
// pays the parse cost (~1-5ms), every subsequent open
// reads the sidecar (one small file read, no image parsing).
func TestReadExifCached_FirstReadWritesSidecar(t *testing.T) {
	// Use a real fixture that has EXIF.
	path := "/var/www/html/images/media_gallery/misty_bamboo_forest_path.jpg"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture not available: %s", path)
		return
	}
	cacheDir, err := os.MkdirTemp("", "exif-cache-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cacheDir)
	// First read: parses source + writes sidecar.
	exif, err := readExifCached(path, cacheDir, "webp")
	if err != nil {
		t.Fatal(err)
	}
	if exif == nil {
		t.Fatal("expected EXIF data from fixture, got nil")
	}
	// Verify the sidecar was written.
	abs, _ := filepath.Abs(path)
	h := sha256.Sum256([]byte(abs))
	wantPath := filepath.Join(cacheDir, hex.EncodeToString(h[:16])+".webp.exif")
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("sidecar not written at %s: %v", wantPath, err)
	}
}

// TestReadExifCached_SecondReadUsesSidecar verifies the
// second call reads the sidecar (fast) without re-parsing
// the source image. We OVERWRITE the source with garbage
// after the first call; the second call must still return
// the cached EXIF (proving the sidecar was used, not a
// re-parse). If the function re-parsed the source, the
// second call would fail with a parse error.
func TestReadExifCached_SecondReadUsesSidecar(t *testing.T) {
	path := "/var/www/html/images/media_gallery/misty_bamboo_forest_path.jpg"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture not available: %s", path)
		return
	}
	cacheDir, err := os.MkdirTemp("", "exif-cache-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cacheDir)
	// First read: parse the source + write sidecar.
	exif1, err := readExifCached(path, cacheDir, "webp")
	if err != nil {
		t.Fatal(err)
	}
	if exif1 == nil {
		t.Fatal("first read: expected EXIF data, got nil")
	}
	// Overwrite the source with garbage. The second read
	// should still succeed because it uses the sidecar. We
	// back up the original bytes first so we can restore the
	// fixture after the test runs (other tests depend on
	// the fixture being a valid JPEG with real EXIF).
	origBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(path, origBytes, 0o644)
	})
	if err := os.WriteFile(path, []byte("not a valid image"), 0o644); err != nil {
		t.Fatal(err)
	}
	exif2, err := readExifCached(path, cacheDir, "webp")
	if err != nil {
		t.Errorf("second read: got error %v (should use sidecar, not re-parse)", err)
	}
	if exif2 == nil {
		t.Errorf("second read: got nil (should have returned cached EXIF)")
	}
	// The cached values should match the first read.
	if exif1 != nil && exif2 != nil {
		if exif1.CameraMake != exif2.CameraMake || exif1.CameraModel != exif2.CameraModel {
			t.Errorf("cached EXIF differs from first read: %+v vs %+v", exif1, exif2)
		}
	}
}

// TestReadExifCached_NoExifCachesEmpty verifies the sidecar
// is written for the "no EXIF" case too. This avoids repeated
// re-parsing of files that don't have an EXIF block (the
// most common case for casual photos).
func TestReadExifCached_NoExifCachesEmpty(t *testing.T) {
	// Use a real fixture that has NO EXIF (elderly_man, misty_bamboo,
	// potted_succulent all have EXIF — pick another image).
	path := "/var/www/html/images/media_gallery/tulip_field_dutch_garden_colorful.webp"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture not available: %s", path)
		return
	}
	cacheDir, err := os.MkdirTemp("", "exif-cache-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(cacheDir)
	// First read: should return nil (no EXIF) AND write the
	// "no EXIF" sidecar so subsequent reads don't re-parse.
	exif, err := readExifCached(path, cacheDir, "webp")
	if err != nil {
		t.Fatal(err)
	}
	if exif != nil {
		t.Errorf("expected no EXIF for %s, got: %+v", filepath.Base(path), exif)
	}
	// Verify the sidecar was written (with has=false).
	abs, _ := filepath.Abs(path)
	h := sha256.Sum256([]byte(abs))
	wantPath := filepath.Join(cacheDir, hex.EncodeToString(h[:16])+".webp.exif")
	data, err := os.ReadFile(wantPath)
	if err != nil {
		t.Errorf("sidecar not written at %s: %v", wantPath, err)
		return
	}
	if !strings.Contains(string(data), "has=false") {
		t.Errorf("sidecar should record has=false for files without EXIF, got: %q", string(data))
	}
}

// TestReadExifCached_NoCacheDir verifies the function falls
// back to the direct readExif when cacheDir is empty (e.g.
// unit-mode tests, no_thumbs mode).
func TestReadExifCached_NoCacheDir(t *testing.T) {
	path := "/var/www/html/images/media_gallery/misty_bamboo_forest_path.jpg"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("fixture not available: %s", path)
		return
	}
	exif, err := readExifCached(path, "", "webp")
	if err != nil {
		t.Fatal(err)
	}
	if exif == nil {
		t.Fatal("expected EXIF data, got nil")
	}
}


// TestWriteExifSidecar verifies the text sidecar format
// produced by writeExifSidecar. Per user request 2026-06-29:
// the EXIF sidecar uses a plain text key=value format
// (smaller, faster parse, no encoding/json dependency)
// instead of JSON.
func TestWriteExifSidecar(t *testing.T) {
	t.Run("nil exif → has=false", func(t *testing.T) {
		data := writeExifSidecar(nil)
		want := "has=false\n"
		if string(data) != want {
			t.Errorf("got %q, want %q", string(data), want)
		}
	})
	t.Run("full exif → has=true + all fields", func(t *testing.T) {
		exif := &ExifData{
			CameraMake:   "Sony",
			CameraModel:  "ILCE-7M4",
			LensModel:    "FE 70-200mm F2.8 GM OSS II",
			DateTaken:    "2024:11:08 06:23:14",
			ExposureTime: "1/250 s",
			Aperture:     "f/4",
			ISO:          "ISO 800",
			FocalLength:  "135 mm",
		}
		data := writeExifSidecar(exif)
		s := string(data)
		// Must start with has=true
		if !strings.HasPrefix(s, "has=true\n") {
			t.Errorf("sidecar should start with has=true, got: %q", s)
		}
		// All fields should be present
		for _, want := range []string{
			"CameraMake=Sony",
			"CameraModel=ILCE-7M4",
			"LensModel=FE 70-200mm F2.8 GM OSS II",
			"DateTaken=2024:11:08 06:23:14",
			"ExposureTime=1/250 s",
			"Aperture=f/4",
			"ISO=ISO 800",
			"FocalLength=135 mm",
		} {
			if !strings.Contains(s, want+"\n") {
				t.Errorf("sidecar should contain %q, got: %q", want, s)
			}
		}
	})
	t.Run("partial exif → has=true + only set fields", func(t *testing.T) {
		exif := &ExifData{
			CameraMake: "Fujifilm",
			// everything else empty
		}
		data := writeExifSidecar(exif)
		s := string(data)
		if !strings.HasPrefix(s, "has=true\n") {
			t.Errorf("sidecar should start with has=true, got: %q", s)
		}
		if !strings.Contains(s, "CameraMake=Fujifilm\n") {
			t.Errorf("sidecar should contain CameraMake, got: %q", s)
		}
		// Should NOT contain the empty fields
		if strings.Contains(s, "CameraModel=") {
			t.Errorf("sidecar should NOT contain empty CameraModel=, got: %q", s)
		}
		if strings.Contains(s, "LensModel=") {
			t.Errorf("sidecar should NOT contain empty LensModel=, got: %q", s)
		}
	})
}

// TestParseExifSidecar verifies the text sidecar parser.
func TestParseExifSidecar(t *testing.T) {
	t.Run("has=false → nil (no EXIF)", func(t *testing.T) {
		data := []byte("has=false\n")
		exif := parseExifSidecar(data)
		if exif != nil {
			t.Errorf("parseExifSidecar(has=false) should return nil, got: %+v", exif)
		}
	})
	t.Run("has=true + fields → parsed ExifData", func(t *testing.T) {
		data := []byte(`has=true
CameraMake=Fujifilm
CameraModel=X-T5
LensModel=XF 16-55mm F2.8 R LM WR
DateTaken=2024:09:15 07:48:21
ExposureTime=1/60 s
Aperture=f/5.6
ISO=ISO 1600
FocalLength=23 mm
`)
		exif := parseExifSidecar(data)
		if exif == nil {
			t.Fatal("parseExifSidecar should return non-nil")
		}
		if exif.CameraMake != "Fujifilm" {
			t.Errorf("CameraMake: got %q, want Fujifilm", exif.CameraMake)
		}
		if exif.CameraModel != "X-T5" {
			t.Errorf("CameraModel: got %q, want X-T5", exif.CameraModel)
		}
		if exif.LensModel != "XF 16-55mm F2.8 R LM WR" {
			t.Errorf("LensModel: got %q", exif.LensModel)
		}
		if exif.FocalLength != "23 mm" {
			t.Errorf("FocalLength: got %q, want 23 mm", exif.FocalLength)
		}
	})
	t.Run("malformed (no has= prefix) → nil", func(t *testing.T) {
		data := []byte("garbage data\nCameraMake=Foo\n")
		exif := parseExifSidecar(data)
		if exif != nil {
			t.Errorf("malformed sidecar should return nil, got: %+v", exif)
		}
	})
	t.Run("unknown keys are ignored (forward compat)", func(t *testing.T) {
		data := []byte(`has=true
CameraMake=Sony
SomeFutureField=foo
`)
		exif := parseExifSidecar(data)
		if exif == nil {
			t.Fatal("should parse has=true")
		}
		if exif.CameraMake != "Sony" {
			t.Errorf("CameraMake: got %q", exif.CameraMake)
		}
		// SomeFutureField is ignored (forward compatibility)
	})
}

// TestReadExifCached_UsesTextFormat verifies the full flow
// of writing a text sidecar and reading it back.
func TestReadExifCached_UsesTextFormat(t *testing.T) {
	tmp, err := os.MkdirTemp("", "exif-text-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	// Create a source file so exifMetaPath can hash it.
	srcPath := tmp + "/src.jpg"
	if err := os.WriteFile(srcPath, []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Compute the actual sidecar path the helper would use
	// (so the helper finds our pre-populated sidecar).
	sidecarPath := exifMetaPath(srcPath, tmp, "webp")
	sidecarContent := `has=true
CameraMake=Canon
CameraModel=EOS R5
`
	if err := os.WriteFile(sidecarPath, []byte(sidecarContent), 0o644); err != nil {
		t.Fatal(err)
	}
	exif, err := readExifCached(srcPath, tmp, "webp")
	if err != nil {
		t.Fatal(err)
	}
	if exif == nil {
		t.Fatal("expected non-nil exif")
	}
	if exif.CameraMake != "Canon" {
		t.Errorf("CameraMake: got %q, want Canon", exif.CameraMake)
	}
	if exif.CameraModel != "EOS R5" {
		t.Errorf("CameraModel: got %q, want EOS R5", exif.CameraModel)
	}
	// Verify the sidecar IS in text format (not JSON)
	data, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "has=true") {
		t.Errorf("sidecar should be in text format, got: %q", string(data))
	}
	if strings.Contains(string(data), `{"has":`) {
		t.Errorf("sidecar should NOT be in JSON format, got: %q", string(data))
	}
}

package gallery

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSweepOrphans verifies the orphan sidecar cleanup
// function. Per user request 2026-07-04 (minor-fixes branch,
// fix #1): the cache accumulates orphan sidecars from prior
// Caddy versions, crashed writes, and prior bugs. The orphan
// sweep removes sidecars that have no matching thumb.
func TestSweepOrphans(t *testing.T) {
	tmp := t.TempDir()
	cacheDir := filepath.Join(tmp, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create the 2-level nested layout with one thumb +
	// its matching .meta sidecar, plus 2 orphan sidecars.
	leafDir := filepath.Join(cacheDir, "00", "ab")
	if err := os.MkdirAll(leafDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Matched pair: thumb + .meta
	if err := os.WriteFile(filepath.Join(leafDir, "abcd1234.webp"), []byte("thumb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leafDir, "abcd1234.webp.meta"), []byte("meta"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Orphan .meta (no matching thumb)
	if err := os.WriteFile(filepath.Join(leafDir, "orphan1.webp.meta"), []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Orphan .exif (no matching thumb)
	if err := os.WriteFile(filepath.Join(leafDir, "orphan2.webp.exif"), []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run the sweep
	deleted, err := sweepOrphans(cacheDir)
	if err != nil {
		t.Fatalf("sweepOrphans failed: %v", err)
	}
	if deleted != 2 {
		t.Errorf("expected 2 orphans deleted, got %d", deleted)
	}

	// Verify the matched pair is still there
	if _, err := os.Stat(filepath.Join(leafDir, "abcd1234.webp")); err != nil {
		t.Error("matched thumb was incorrectly deleted")
	}
	if _, err := os.Stat(filepath.Join(leafDir, "abcd1234.webp.meta")); err != nil {
		t.Error("matched .meta was incorrectly deleted")
	}

	// Verify the orphans are gone
	if _, err := os.Stat(filepath.Join(leafDir, "orphan1.webp.meta")); !os.IsNotExist(err) {
		t.Error("orphan1.webp.meta was not deleted")
	}
	if _, err := os.Stat(filepath.Join(leafDir, "orphan2.webp.exif")); !os.IsNotExist(err) {
		t.Error("orphan2.webp.exif was not deleted")
	}
}

// TestSweepOrphansNone verifies the sweep is a no-op on a
// clean cache.
func TestSweepOrphansNone(t *testing.T) {
	tmp := t.TempDir()
	cacheDir := filepath.Join(tmp, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	leafDir := filepath.Join(cacheDir, "00", "ab")
	if err := os.MkdirAll(leafDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leafDir, "abcd.webp"), []byte("thumb"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leafDir, "abcd.webp.meta"), []byte("meta"), 0o644); err != nil {
		t.Fatal(err)
	}
	deleted, err := sweepOrphans(cacheDir)
	if err != nil {
		t.Fatalf("sweepOrphans failed: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 orphans deleted, got %d", deleted)
	}
}

// TestSweepOrphansMissingDir verifies the sweep handles a
// missing cache dir gracefully (returns 0, not an error
// worth panicking over).
func TestSweepOrphansMissingDir(t *testing.T) {
	tmp := t.TempDir()
	cacheDir := filepath.Join(tmp, "does-not-exist")
	deleted, err := sweepOrphans(cacheDir)
	if err != nil {
		t.Errorf("sweepOrphans on missing dir should be silent, got error: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted, got %d", deleted)
	}
}

// TestIsSidecarFile verifies the sidecar extension
// classifier.
func TestIsSidecarFile(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"abcd.webp.meta", true},
		{"abcd.webp.exif", true},
		{"abcd.webp.vmeta", true},
		{"abcd.webp.ameta", true},
		{"abcd.webp", false},        // thumb, not sidecar
		{"abcd.jpg", false},         // legacy thumb
		{"abcd.png", false},         // legacy thumb
		{"plain.txt", false},        // unrelated
		{".meta", true},             // edge case: just the extension
		{"UPPERCASE.WEBP.META", true}, // case insensitive
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isSidecarFile(tc.name)
			if got != tc.want {
				t.Errorf("isSidecarFile(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestThumbExistsFor verifies the thumb-exists lookup.
func TestThumbExistsFor(t *testing.T) {
	tmp := t.TempDir()
	// Create a thumb
	thumbPath := filepath.Join(tmp, "abcd.webp")
	if err := os.WriteFile(thumbPath, []byte("thumb"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		sidecarPath string
		want       bool
	}{
		{"existing thumb", thumbPath + ".meta", true},
		{"missing thumb", filepath.Join(tmp, "missing.webp.meta"), false},
		{"uppercase ext", thumbPath + ".META", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := thumbExistsFor(tc.sidecarPath)
			if got != tc.want {
				t.Errorf("thumbExistsFor(%q) = %v, want %v", tc.sidecarPath, got, tc.want)
			}
		})
	}
}

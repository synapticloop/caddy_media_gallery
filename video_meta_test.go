package gallery

import (
	"os"
	"path/filepath"
	"testing"
)

// TestVideoMeta_HasAny verifies that HasAny correctly
// distinguishes an empty VideoMeta (no metadata) from one
// with at least one populated field.
func TestVideoMeta_HasAny(t *testing.T) {
	// Empty — no metadata, should return false.
	empty := &VideoMeta{}
	if empty.HasAny() {
		t.Error("expected empty VideoMeta.HasAny() == false")
	}

	// Nil pointer — also no metadata.
	var nilMeta *VideoMeta
	if nilMeta.HasAny() {
		t.Error("expected nil VideoMeta.HasAny() == false")
	}

	// One field populated — should return true.
	one := &VideoMeta{Duration: "0:05"}
	if !one.HasAny() {
		t.Error("expected VideoMeta with Duration set.HasAny() == true")
	}

	// Each field individually (sanity check).
	for _, m := range []VideoMeta{
		{Container: "mov,mp4"},
		{VideoCodec: "h264"},
		{AudioCodec: "aac"},
		{Bitrate: "5.2 Mbps"},
		{Framerate: "24 fps"},
	} {
		if !m.HasAny() {
			t.Errorf("expected VideoMeta with %+v to have HasAny() == true", m)
		}
	}
}

// TestWriteVideoMetaSidecar_Nil verifies that writing
// a nil *VideoMeta produces a "has=false\n" sidecar
// (a valid "no data" cached result, not an error).
func TestWriteVideoMetaSidecar_Nil(t *testing.T) {
	data := writeVideoMetaSidecar(nil)
	if string(data) != "has=false\n" {
		t.Errorf("expected nil meta to produce has=false, got %q", string(data))
	}
}

// TestWriteVideoMetaSidecar_Empty verifies that writing
// an empty (but non-nil) VideoMeta produces a "has=false\n"
// sidecar (no fields populated means no metadata to cache).
func TestWriteVideoMetaSidecar_Empty(t *testing.T) {
	empty := &VideoMeta{}
	data := writeVideoMetaSidecar(empty)
	if string(data) != "has=false\n" {
		t.Errorf("expected empty meta to produce has=false, got %q", string(data))
	}
}

// TestWriteAndParseVideoMetaSidecar_Roundtrip verifies
// the sidecar round-trip: writeVideoMetaSidecar produces
// output that parseVideoMetaSidecar can correctly parse
// back into an equivalent struct.
func TestWriteAndParseVideoMetaSidecar_Roundtrip(t *testing.T) {
	original := &VideoMeta{
		Duration:   "1:23",
		Container:  "mov,mp4,m4a,3gp,3g2,mj2",
		VideoCodec: "h264",
		AudioCodec: "aac",
		Bitrate:    "5.2 Mbps",
		Framerate:  "24 fps",
	}
	data := writeVideoMetaSidecar(original)
	parsed := parseVideoMetaSidecar(data)
	if parsed == nil {
		t.Fatalf("parseVideoMetaSidecar returned nil for valid sidecar data: %q", string(data))
	}
	if parsed.Duration != original.Duration {
		t.Errorf("Duration: got %q, want %q", parsed.Duration, original.Duration)
	}
	if parsed.Container != original.Container {
		t.Errorf("Container: got %q, want %q", parsed.Container, original.Container)
	}
	if parsed.VideoCodec != original.VideoCodec {
		t.Errorf("VideoCodec: got %q, want %q", parsed.VideoCodec, original.VideoCodec)
	}
	if parsed.AudioCodec != original.AudioCodec {
		t.Errorf("AudioCodec: got %q, want %q", parsed.AudioCodec, original.AudioCodec)
	}
	if parsed.Bitrate != original.Bitrate {
		t.Errorf("Bitrate: got %q, want %q", parsed.Bitrate, original.Bitrate)
	}
	if parsed.Framerate != original.Framerate {
		t.Errorf("Framerate: got %q, want %q", parsed.Framerate, original.Framerate)
	}
}

// TestParseVideoMetaSidecar_HasFalse verifies that a
// "has=false" sidecar parses back to a nil *VideoMeta
// (a valid "no data" cached result).
func TestParseVideoMetaSidecar_HasFalse(t *testing.T) {
	data := []byte("has=false\n")
	parsed := parseVideoMetaSidecar(data)
	if parsed != nil {
		t.Errorf("expected nil for has=false sidecar, got %+v", parsed)
	}
}

// TestParseVideoMetaSidecar_Malformed verifies that a
// malformed sidecar (no "has=..." header) parses to nil
// (caller falls through to a fresh read).
func TestParseVideoMetaSidecar_Malformed(t *testing.T) {
	// No newline
	if p := parseVideoMetaSidecar([]byte("garbage")); p != nil {
		t.Errorf("expected nil for garbage input, got %+v", p)
	}
	// Wrong header
	if p := parseVideoMetaSidecar([]byte("wrong=header\nfoo=bar\n")); p != nil {
		t.Errorf("expected nil for wrong header, got %+v", p)
	}
	// Empty input
	if p := parseVideoMetaSidecar([]byte{}); p != nil {
		t.Errorf("expected nil for empty input, got %+v", p)
	}
}

// TestFormatDuration verifies that the human-readable
// duration formatter produces the expected "M:SS" or
// "H:MM:SS" output from ffprobe's seconds input.
func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"5.875000", "0:05"},
		{"59.5", "0:59"},
		{"60", "1:00"},
		{"125", "2:05"},
		{"3661", "1:01:01"},
		{"7325", "2:02:05"},
		{"N/A", ""},
		{"", ""},
		{"abc", ""},
		{"-5", ""},
	}
	for _, c := range cases {
		got := formatDuration(c.in)
		if got != c.want {
			t.Errorf("formatDuration(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFormatBitrate verifies that the human-readable
// bitrate formatter produces the expected "X.X Mbps" or
// "X kbps" output from ffprobe's bps input.
func TestFormatBitrate(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"1073568", "1.1 Mbps"},
		{"500000", "500 kbps"},
		{"1000000000", "1000.0 Mbps"},
		{"500", "500 bps"},
		{"0", ""},
		{"abc", ""},
		{"", ""},
		{"N/A", ""},
	}
	for _, c := range cases {
		got := formatBitrate(c.in)
		if got != c.want {
			t.Errorf("formatBitrate(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFormatFramerate verifies that the framerate formatter
// handles the three common formats from ffprobe:
// fraction ("24/1", "30000/1001"), decimal ("59.94"),
// and unparseable (returns raw string with " fps" suffix).
func TestFormatFramerate(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"24/1", "24 fps"},
		{"30000/1001", "29.97 fps"},
		{"60/1", "60 fps"},
		{"59.94", "59.94 fps"},
		{"25/1", "25 fps"},
		{"0/0", ""},
		{"", ""},
		{"garbage", "garbage fps"},
	}
	for _, c := range cases {
		got := formatFramerate(c.in)
		if got != c.want {
			t.Errorf("formatFramerate(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestVideoMetaCached_RealFixture verifies the full
// readVideoMetaCached flow against a real video file
// from /var/www/html/images/videoqueue/. The flow:
//   1. First call: no sidecar exists, so readVideoMeta
//      runs ffprobe and writes a new sidecar.
//   2. Second call: sidecar exists, parseVideoMetaSidecar
//      reads it back without invoking ffprobe.
//
// The test asserts:
//   - First call returns a non-nil *VideoMeta with
//     the expected field values
//   - Sidecar file is created on disk
//   - Second call returns the same values (sidecar
//     round-trip preserves the data)
//   - t.Cleanup removes the sidecar so the test is
//     idempotent across runs
func TestVideoMetaCached_RealFixture(t *testing.T) {
	const srcPath = "/var/www/html/images/videoqueue/vid_q00000006_409300050727226.mp4"
	// Skip the test if the fixture file doesn't exist
	// (e.g. on a fresh checkout). The test relies on a
	// real video file with known ffprobe output.
	if _, err := os.Stat(srcPath); err != nil {
		t.Skipf("fixture file not found: %s (%v)", srcPath, err)
	}

	// Use a temp dir for the sidecar. t.TempDir() creates
	// a temp dir and cleans it up automatically.
	cacheDir := t.TempDir()
	thumbExt := "webp"

	// Clean up any pre-existing sidecar from a previous run
	metaPath := videoMetaPath(srcPath, cacheDir, thumbExt)
	_ = os.Remove(metaPath)

	// First call — fresh read via ffprobe
	meta1, err := readVideoMetaCached(srcPath, cacheDir, thumbExt)
	if err != nil {
		t.Fatalf("readVideoMetaCached (first call) failed: %v", err)
	}
	if meta1 == nil {
		t.Fatal("expected non-nil *VideoMeta for valid video file")
	}
	if meta1.VideoCodec == "" {
		t.Errorf("expected non-empty VideoCodec, got %q", meta1.VideoCodec)
	}
	if meta1.Framerate == "" {
		t.Errorf("expected non-empty Framerate, got %q", meta1.Framerate)
	}
	if meta1.Container == "" {
		t.Errorf("expected non-empty Container, got %q", meta1.Container)
	}

	// Sidecar should now exist
	if _, err := os.Stat(metaPath); err != nil {
		t.Errorf("expected sidecar at %s, got: %v", metaPath, err)
	}

	// Second call — reads from sidecar (no ffprobe invocation)
	meta2, err := readVideoMetaCached(srcPath, cacheDir, thumbExt)
	if err != nil {
		t.Fatalf("readVideoMetaCached (second call) failed: %v", err)
	}
	if meta2 == nil {
		t.Fatal("expected non-nil *VideoMeta on second call (sidecar read)")
	}
	if meta2.VideoCodec != meta1.VideoCodec {
		t.Errorf("VideoCodec mismatch: got %q, want %q", meta2.VideoCodec, meta1.VideoCodec)
	}
	if meta2.Framerate != meta1.Framerate {
		t.Errorf("Framerate mismatch: got %q, want %q", meta2.Framerate, meta1.Framerate)
	}
	if meta2.Container != meta1.Container {
		t.Errorf("Container mismatch: got %q, want %q", meta2.Container, meta1.Container)
	}

	// Cleanup: remove the sidecar (t.TempDir handles this,
	// but explicit cleanup is also fine for clarity).
	_ = os.Remove(metaPath)
	_ = filepath.Dir(metaPath) // keep the import
}

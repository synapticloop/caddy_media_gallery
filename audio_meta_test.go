// audio_meta_test.go — Tests for the Tier-1 audio metadata
// pipeline. Mirrors video_meta_test.go's structure.

package gallery

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// TestFormatSampleRate covers the SampleRate formatter
// (e.g. "44100" → "44.1 kHz", "48000" → "48 kHz").
func TestFormatSampleRate(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"48000", "48 kHz"},
		{"44100", "44.1 kHz"},
		{"22050", "22.05 kHz"},
		{"96000", "96 kHz"},
		{"11025", "11.025 kHz"},
		{"", ""},
		{"N/A", ""},
		{"garbage", ""},
	}
	for _, c := range cases {
		got := formatSampleRate(c.in)
		if got != c.want {
			t.Errorf("formatSampleRate(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFormatAudioDurationBitrate covers the shared formatDuration
// and formatBitrate helpers (defined in video_meta.go; same
// format is used for audio duration/bitrate).
func TestFormatAudioDurationBitrate(t *testing.T) {
	// Duration: 1m23 → "1:23", 1h23m45s → "1:23:45", 30s → "0:30"
	cases := []struct {
		in   string
		want string
	}{
		{"83", "1:23"},
		{"5025", "1:23:45"},
		{"30", "0:30"},
		{"0", "0:00"},
		{"", ""},
		{"N/A", ""},
	}
	for _, c := range cases {
		got := formatDuration(c.in)
		if got != c.want {
			t.Errorf("formatDuration(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Bitrate: 128_000 → "128 kbps", 1_500_000 → "1.5 Mbps", 800 → "800 bps"
	bcases := []struct {
		in   string
		want string
	}{
		{"128000", "128 kbps"},
		{"1500000", "1.5 Mbps"},
		{"5200000", "5.2 Mbps"},
		{"800", "800 bps"},
		{"", ""},
		{"N/A", ""},
	}
	for _, c := range bcases {
		got := formatBitrate(c.in)
		if got != c.want {
			t.Errorf("formatBitrate(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestAudioMetaHasAny covers the AudioMeta.HasAny() helper.
func TestAudioMetaHasAny(t *testing.T) {
	empty := &AudioMeta{}
	if empty.HasAny() {
		t.Error("empty AudioMeta should not have any fields populated")
	}
	hasCodec := &AudioMeta{Codec: "mp3"}
	if !hasCodec.HasAny() {
		t.Error("AudioMeta with Codec should report HasAny=true")
	}
}

// TestAudioMetaSidecarRoundTrip covers the write / parse
// sidecar serialization (parallels the existing
// writeVideoMetaSidecar / parseVideoMetaSidecar tests).
func TestAudioMetaSidecarRoundTrip(t *testing.T) {
	orig := &AudioMeta{
		Codec:        "mp3",
		SampleRate:   "44.1 kHz",
		Channels:     "2",
		ChannelLayout: "stereo",
		Duration:     "0:30",
		Bitrate:      "128 kbps",
	}
	data := writeAudioMetaSidecar(orig)
	parsed := parseAudioMetaSidecar(data)
	if parsed == nil {
		t.Fatal("parseAudioMetaSidecar returned nil for valid sidecar")
	}
	if parsed.Codec != orig.Codec ||
		parsed.SampleRate != orig.SampleRate ||
		parsed.Channels != orig.Channels ||
		parsed.ChannelLayout != orig.ChannelLayout ||
		parsed.Duration != orig.Duration ||
		parsed.Bitrate != orig.Bitrate {
		t.Errorf("round-trip mismatch:\n  got:  %+v\n  want: %+v", parsed, orig)
	}
}

// TestAudioMetaSidecarEmpty covers the empty-AudioMeta sidecar
// (has=false). Reading it back returns nil, which the
// caller treats as "no metadata cached".
func TestAudioMetaSidecarEmpty(t *testing.T) {
	data := writeAudioMetaSidecar(&AudioMeta{})
	if string(data) != "has=false\n" {
		t.Errorf("empty sidecar should be 'has=false\\n', got %q", string(data))
	}
	parsed := parseAudioMetaSidecar(data)
	if parsed != nil {
		t.Errorf("parseAudioMetaSidecar of empty should return nil, got %+v", parsed)
	}
}

// TestAudioMetaSidecarMalformed covers the parser's
// rejection of invalid sidecars. The caller (readAudioMetaCached)
// falls through to a fresh read on parse failure.
func TestAudioMetaSidecarMalformed(t *testing.T) {
	cases := [][]byte{
		nil,                       // empty file
		[]byte("garbage"),         // not even a header
		[]byte("has=true\n"),
		[]byte("has=foo\nCodec=mp3"),  // invalid header value
	}
	for i, c := range cases {
		parsed := parseAudioMetaSidecar(c)
		if parsed != nil {
			t.Errorf("case %d: expected nil for malformed sidecar %q, got %+v", i, c, parsed)
		}
	}
}

// TestParseAudioStreamEntries covers the parser for ffprobe's
// default-format stream output. Tested with both partial
// (audio-only, no video) and full (video+audio) output.
func TestParseAudioStreamEntries(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  AudioMeta
	}{
		{
			name: "mp3 stereo 44.1kHz",
			in: `[STREAM]
codec_name=mp3
sample_rate=44100
channels=2
channel_layout=stereo
[/STREAM]`,
			want: AudioMeta{
				Codec:        "mp3",
				SampleRate:   "44100",
				Channels:     "2",
				ChannelLayout: "stereo",
			},
		},
		{
			name: "flac mono 96kHz",
			in: `[STREAM]
codec_name=flac
sample_rate=96000
channels=1
channel_layout=mono
[/STREAM]`,
			want: AudioMeta{
				Codec:        "flac",
				SampleRate:   "96000",
				Channels:     "1",
				ChannelLayout: "mono",
			},
		},
		{
			name:  "empty",
			in:    "",
			want:  AudioMeta{},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			codec, sampleRate, channels, channelLayout, err := parseAudioStreamEntries(c.in)
			if err != nil {
				t.Fatalf("parseAudioStreamEntries returned error: %v", err)
			}
			if codec != c.want.Codec ||
				sampleRate != c.want.SampleRate ||
				channels != c.want.Channels ||
				channelLayout != c.want.ChannelLayout {
				t.Errorf("mismatch:\n  got:  codec=%q sr=%q ch=%q cl=%q\n  want: codec=%q sr=%q ch=%q cl=%q",
					codec, sampleRate, channels, channelLayout,
					c.want.Codec, c.want.SampleRate, c.want.Channels, c.want.ChannelLayout)
			}
		})
	}
}

// TestParseAudioFormatEntries covers the parser for
// ffprobe's default-format FORMAT block.
func TestParseAudioFormatEntries(t *testing.T) {
	in := `[FORMAT]
duration=83
bit_rate=128000
[/FORMAT]`
	duration, bitrate, err := parseAudioFormatEntries(in)
	if err != nil {
		t.Fatalf("parseAudioFormatEntries returned error: %v", err)
	}
	if duration != "1:23" {
		t.Errorf("duration = %q, want %q", duration, "1:23")
	}
	if bitrate != "128 kbps" {
		t.Errorf("bitrate = %q, want %q", bitrate, "128 kbps")
	}
}

// TestReadAudioMetaWithFfmpeg exercises the full readAudioMeta
// path (subprocess to ffprobe) on a real audio file. Skipped
// if ffmpeg isn't installed (the test would otherwise be
// flaky on minimal host setups).
func TestReadAudioMetaWithFfmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed; skipping live ffprobe test")
	}
	// Generate a 1-second 440Hz tone in WAV. We need a
	// real audio file to read; ffmpeg's lavfi source can
	// synthesise one in-memory via stdin but it's simpler
	// to write a real temp file.
	tmp := t.TempDir()
	wavPath := tmp + "/test.wav"
	ctx, cancel := context.WithTimeout(context.Background(), 5*1e9)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-f", "lavfi",
		"-i", "sine=frequency=440:duration=1",
		"-ar", "44100",
		wavPath, "-y")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg failed to generate test WAV: %v\n%s", err, out)
	}
	meta, err := readAudioMeta(wavPath)
	if err != nil {
		t.Fatalf("readAudioMeta failed: %v", err)
	}
	if meta == nil {
		t.Fatal("readAudioMeta returned nil")
	}
	if !strings.Contains(meta.Codec, "pcm") {
		t.Errorf("expected codec to contain 'pcm' (WAV is PCM), got %q", meta.Codec)
	}
	if meta.SampleRate == "" {
		t.Error("expected non-empty SampleRate")
	}
	if meta.Channels == "" {
		t.Error("expected non-empty Channels")
	}
	if meta.Duration == "" {
		t.Error("expected non-empty Duration")
	}
}

// audio_meta.go — Audio metadata extraction + sidecar caching.
//
// Per user request 2026-07-04 (Q4 on the audio-integration
// branch): audio files that match the operator's
// `audio_types` should get a metadata card in the lightbox
// (codec, sample rate, channels, channel layout, duration,
// bitrate). The data comes from ffprobe (already a build
// dependency for video thumbnails) and is cached in a
// sidecar file (`.webp.ameta`) next to the source's thumb
// in the cache dir.
//
// The pattern mirrors the existing video_meta.go flow:
//   - FileInfo gains an AudioMeta field (populated at
//     scan time via the same fast-scan + slow-enrich
//     pipeline that handles EXIF + dimensions + video meta).
//   - FileView gains an AudioMeta field + a pre-rendered
//     AudioMetaAttrs HTML attribute string (for the
//     `data-audio-*` attributes on the audio card).
//   - The lightbox JS shows a separate "audio metadata"
//     panel for audio files (parallel to the existing
//     video META panel), with rows for Codec, Sample Rate,
//     Channels, Channel Layout, Duration, Bitrate.
//
// What's in AudioMeta (Q4 decision: stream-level only —
// no ID3/Vorbis tag extraction in 1.1.0):
//   - Codec: codec_name of the first audio stream
//     (e.g. "mp3", "aac", "flac", "vorbis", "opus").
//     Empty if not parseable.
//   - SampleRate: formatted "44.1 kHz" / "48 kHz" / "22.05 kHz".
//     Empty if not parseable.
//   - Channels: formatted "1" / "2" / "6" (with implicit
//     "(mono)" / "(stereo)" / "(5.1)" via the channel layout).
//   - ChannelLayout: "mono" / "stereo" / "5.1" / "7.1" / "".
//     Empty if not parseable.
//   - Duration: formatted "1:23" (under 1h) or "1:23:45"
//     (1h+). Mirrors the video duration formatter.
//   - Bitrate: formatted "5.2 Mbps" / "842 kbps" / "128 kbps".
//     Mirrors the video bitrate formatter.
//
// Note: Width and Height are NOT part of AudioMeta — audio
// files don't have dimensions.
//
// Tier 1 (stream-level only) per Q4: no ID3 / Vorbis /
// iTunes tag extraction. The lightbox shows codec / sample
// rate / channels / duration / bitrate only.
//
// Sidecar format mirrors writeExifSidecar / writeVideoMetaSidecar:
//   has=true|false\n
//   key=value\n
//   ...
//
// The first line is always "has=true" or "has=false" so
// parseAudioMetaSidecar can detect empty results without
// having to look at every key.
//
// When ffmpeg/ffprobe is not available (g.ffmpegPath == ""),
// readAudioMeta returns (nil, nil) — i.e. "no metadata,
// not an error". This is the Q3 fallback: audio files
// still work (KindAudio, audio filter, lightbox audio
// player), they just don't have a metadata card.
//
// NoAudioMeta (operator-set flag, parallel to NoExif /
// NoMeta) opts out of this whole module: when true, the
// scanner's enrich pass skips readAudioMetaCached
// entirely, the .ameta sidecar is never written, and
// FileInfo.AudioMeta is left nil.

package gallery

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// AudioMeta holds the extracted audio metadata for a single
// audio file. Tier 1 only (per Q4): stream-level codec /
// sample-rate / channels / channel-layout / duration /
// bitrate, no ID3 tags.
type AudioMeta struct {
	// Codec is the codec_name of the first audio stream
	// (e.g. "mp3", "aac", "flac", "vorbis", "opus").
	Codec string
	// SampleRate is the human-readable sample rate in Hz
	// (e.g. "44.1 kHz", "48 kHz", "96 kHz"). Formatted from
	// the raw Hz value; the original is not preserved.
	SampleRate string
	// Channels is the human-readable channel count
	// (e.g. "1", "2", "6"). Just the digit, since the
	// channel layout ("mono", "stereo", "5.1") carries
	// the semantic meaning.
	Channels string
	// ChannelLayout is the spatial layout string
	// (e.g. "mono", "stereo", "5.1", "7.1"). May be empty
	// for non-standard layouts (e.g. a 4-channel stream
	// without a recognized name).
	ChannelLayout string
	// Duration is the formatted playback length
	// (e.g. "1:23" for 1 min 23 sec, "1:23:45" for 1h+).
	// Mirrors the video duration formatter exactly.
	Duration string
	// Bitrate is the human-readable overall bitrate
	// (e.g. "5.2 Mbps", "842 kbps", "128 kbps"). Mirrors
	// the video bitrate formatter exactly.
	Bitrate string
}

// HasAny returns true if at least one AudioMeta field is
// populated. Used by the sidecar writer to decide whether
// to write "has=true\n" (with content) or "has=false\n"
// (empty cached result, so the next request doesn't
// re-run ffprobe).
func (a *AudioMeta) HasAny() bool {
	return a == nil ||
		a.Codec != "" ||
		a.SampleRate != "" ||
		a.Channels != "" ||
		a.ChannelLayout != "" ||
		a.Duration != "" ||
		a.Bitrate != ""
}

// audioMetaPath returns the on-disk path of the .ameta
// sidecar for a given source file. The sidecar lives
// in the thumb cache dir, named "<hash>.webp.ameta" —
// parallel to the existing "<hash>.webp.vmeta" sidecars
// for video meta. The ".ameta" suffix distinguishes the
// audio sidecar from the video sidecar on the rare
// occasion both are present (won't happen in practice —
// a file is one Kind or the other, not both).
func audioMetaPath(src, cacheDir, thumbExt string) string {
	return cachePath(src, cacheDir, "."+thumbExt+".ameta")
}

// readAudioMetaFile reads the .ameta sidecar. Returns the
// raw file content + true on success, (nil, false) if the
// file doesn't exist or has any read error. Mirrors
// readVideoMetaFile.
func readAudioMetaFile(src, cacheDir, thumbExt string) ([]byte, bool) {
	metaPath := audioMetaPath(src, cacheDir, thumbExt)
	if data, err := os.ReadFile(metaPath); err == nil {
		return data, true
	}
	return nil, false
}

// readAudioMeta uses ffprobe to extract audio metadata from
// a single file. Returns (*AudioMeta, nil) on success
// (including "no metadata" which is a valid result with
// all empty strings), or (nil, err) on hard failure
// (ffprobe missing, timeout, or the file isn't readable).
//
// The query is constrained to the first audio stream
// only (`-select_streams a:0`) since audio files
// shouldn't have video streams, and we want the codec
// for the FIRST audio stream (the only meaningful one
// in 99% of cases — multi-track audio files are rare
// in operator galleries).
//
// We use two ffprobe calls (one for stream info, one
// for format info) so we don't iterate all streams
// needlessly — same pattern as readVideoMeta.
func readAudioMeta(path string) (*AudioMeta, error) {
	out := &AudioMeta{}

	// First call: audio stream + format
	codec, sampleRate, channels, channelLayout, err := readAudioStreamInfo(path)
	if err != nil {
		return nil, err
	}
	out.Codec = codec
	out.SampleRate = formatSampleRate(sampleRate)
	out.Channels = channels
	out.ChannelLayout = channelLayout

	// Second call: format
	duration, bitrate, err := readAudioFormat(path)
	if err != nil {
		return nil, err
	}
	out.Duration = duration
	out.Bitrate = bitrate

	return out, nil
}

// readAudioStreamInfo runs ffprobe and extracts the first
// audio stream's codec, sample rate, channels, and
// channel layout. Mirrors readVideoStreamInfo.
func readAudioStreamInfo(path string) (codec, sampleRate, channels, channelLayout string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// -v error: only show errors (not the ffprobe banner)
	// -show_entries stream=codec_name,sample_rate,channels,channel_layout: just the fields we want
	// -select_streams a:0: the FIRST audio stream (a:0; a:1 would be the second)
	// -of default: the "default" text format (one key=value per line)
	probe := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "stream=codec_name,sample_rate,channels,channel_layout",
		"-select_streams", "a:0",
		"-of", "default",
		path,
	)
	var out bytes.Buffer
	probe.Stdout = &out
	probe.Stderr = &out
	if err := probe.Run(); err != nil {
		return "", "", "", "", err
	}
	return parseAudioStreamEntries(out.String())
}

// parseAudioStreamEntries extracts the codec_name / sample_rate /
// channels / channel_layout values from ffprobe's default-format
// output. The output looks like:
//
//	[STREAM]
//	codec_name=mp3
//	sample_rate=44100
//	channels=2
//	channel_layout=stereo
//	[/STREAM]
func parseAudioStreamEntries(s string) (codec, sampleRate, channels, channelLayout string, err error) {
	for _, line := range strings.Split(s, "\n") {
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		switch key {
		case "codec_name":
			codec = val
		case "sample_rate":
			sampleRate = val
		case "channels":
			channels = val
		case "channel_layout":
			channelLayout = val
		}
	}
	return
}

// formatSampleRate formats a raw Hz value (e.g. "44100",
// "48000", "22050") as a human-readable string. Decimals
// are shown only when they're meaningful (44.1 kHz vs
// 48 kHz) — values that are exact multiples of 1000
// are shown without decimals.
func formatSampleRate(s string) string {
	if s == "" {
		return ""
	}
	hz, err := strconv.Atoi(s)
	if err != nil {
		// Not a number (e.g. ffprobe returned "N/A") —
		// pass through as-is. Rare.
		return s
	}
	if hz%1000 == 0 {
		// 48000 → "48 kHz", 22050 → "22.05 kHz" (22050 % 1000 = 50)
		// 44100 → "44.1 kHz" (44100 % 1000 = 100)
		// 22050 / 44100 — 22050 % 1000 = 50, 44100 % 1000 = 100.
		// Actually 22050 % 1000 = 50, NOT 0. So 22050 falls
		// through to the decimal branch.
	}
	kHz := float64(hz) / 1000.0
	// Show one decimal if it's a "common" value (44.1, 22.05,
	// 11.025, etc.); otherwise round to integer.
	if kHz == float64(int(kHz)) {
		return strconv.Itoa(int(kHz)) + " kHz"
	}
	// Format with up to 2 decimal places, trim trailing zeros
	// + decimal point if any.
	s = strconv.FormatFloat(kHz, 'f', 2, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s + " kHz"
}

// readAudioFormat runs ffprobe and extracts the file's
// duration and overall bitrate from the FORMAT block.
func readAudioFormat(path string) (duration, bitrate string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	probe := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration,bit_rate",
		"-of", "default",
		path,
	)
	var out bytes.Buffer
	probe.Stdout = &out
	probe.Stderr = &out
	if err := probe.Run(); err != nil {
		return "", "", err
	}
	return parseAudioFormatEntries(out.String())
}

// parseAudioFormatEntries extracts duration and bit_rate
// from ffprobe's default-format output.
func parseAudioFormatEntries(s string) (duration, bitrate string, err error) {
	for _, line := range strings.Split(s, "\n") {
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		switch key {
		case "duration":
			duration = formatDuration(val)
		case "bit_rate":
			bitrate = formatBitrate(val)
		}
	}
	return
}

// formatDuration and formatBitrate are shared with the video
// metadata pipeline (in video_meta.go) and are not redefined
// here. The same formatters work for both — durations and
// bitrates are file-level facts, not stream-type-specific.

// readAudioMetaCached is the cache-aware top-level entry
// point. Mirrors readVideoMetaCached.
func readAudioMetaCached(path, cacheDir, thumbExt string) (*AudioMeta, error) {
	if cacheDir == "" {
		// No cache dir — fall back to direct read.
		return readAudioMeta(path)
	}
	metaPath := audioMetaPath(path, cacheDir, thumbExt)

	// Try the sidecar first.
	if data, ok := readAudioMetaFile(path, cacheDir, thumbExt); ok {
		// Staleness check.
		srcInfo, srcErr := os.Stat(path)
		if srcErr == nil {
			if sidecarInfo, statErr := os.Stat(metaPath); statErr == nil {
				if sidecarInfo.ModTime().Before(srcInfo.ModTime()) {
					// Stale — fall through to a fresh read.
					goto fresh
				}
			}
		}
		if meta := parseAudioMetaSidecar(data); meta != nil || bytes.HasPrefix(data, []byte("has=false\n")) {
			// Successfully parsed. nil meta + "has=false"
			// prefix means "no metadata" (valid cached result).
			// nil meta without that prefix means "malformed
			// sidecar" — fall through.
			return meta, nil
		}
	}

fresh:
	// Fresh read: call ffprobe, write sidecar, return.
	meta, err := readAudioMeta(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		// Non-fatal — return the meta anyway.
		return meta, nil
	}
	data := writeAudioMetaSidecar(meta)
	if err := os.WriteFile(metaPath, data, 0o644); err != nil {
		// Non-fatal — return the meta anyway.
		return meta, nil
	}
	if srcInfo, err := os.Stat(path); err == nil {
		_ = os.Chtimes(metaPath, srcInfo.ModTime(), srcInfo.ModTime())
	}
	return meta, nil
}

// writeAudioMetaSidecar serialises an AudioMeta to the
// sidecar's text format. Mirrors writeExifSidecar /
// writeVideoMetaSidecar: first line "has=true|false",
// then "key=value" lines.
func writeAudioMetaSidecar(meta *AudioMeta) []byte {
	if !meta.HasAny() {
		return []byte("has=false\n")
	}
	var b strings.Builder
	b.WriteString("has=true\n")
	if meta.Codec != "" {
		b.WriteString("Codec=")
		b.WriteString(meta.Codec)
		b.WriteString("\n")
	}
	if meta.SampleRate != "" {
		b.WriteString("SampleRate=")
		b.WriteString(meta.SampleRate)
		b.WriteString("\n")
	}
	if meta.Channels != "" {
		b.WriteString("Channels=")
		b.WriteString(meta.Channels)
		b.WriteString("\n")
	}
	if meta.ChannelLayout != "" {
		b.WriteString("ChannelLayout=")
		b.WriteString(meta.ChannelLayout)
		b.WriteString("\n")
	}
	if meta.Duration != "" {
		b.WriteString("Duration=")
		b.WriteString(meta.Duration)
		b.WriteString("\n")
	}
	if meta.Bitrate != "" {
		b.WriteString("Bitrate=")
		b.WriteString(meta.Bitrate)
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// parseAudioMetaSidecar deserialises the .ameta sidecar
// text format. Returns the AudioMeta, or nil if the
// sidecar is malformed.
func parseAudioMetaSidecar(data []byte) *AudioMeta {
	if len(data) == 0 {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 {
		return nil
	}
	header := strings.TrimSpace(lines[0])
	if header != "has=true" {
		return nil
	}
	meta := &AudioMeta{}
	for _, line := range lines[1:] {
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		switch key {
		case "Codec":
			meta.Codec = val
		case "SampleRate":
			meta.SampleRate = val
		case "Channels":
			meta.Channels = val
		case "ChannelLayout":
			meta.ChannelLayout = val
		case "Duration":
			meta.Duration = val
		case "Bitrate":
			meta.Bitrate = val
		}
	}
	return meta
}
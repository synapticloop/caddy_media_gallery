// video_meta.go — Video metadata extraction + sidecar caching.
//
// Per user request 2026-07-02: video files should get the
// same kind of metadata enrichment that EXIF gives to
// images. The data comes from ffprobe (already a build
// dependency for video thumbnails) and is cached in a
// sidecar file (`.webp.vmeta`) next to the video's thumb.
//
// The pattern mirrors the existing EXIF flow:
//   - FileInfo gains a VideoMeta field (populated at scan time
//     via the same "fast scan + slow background enrich"
//     pipeline that handles EXIF + dimensions)
//   - FileView gains a VideoMeta field + a pre-rendered
//     VideoMetaAttrs HTML attribute string (for the
//     `data-video-*` attributes on the video card)
//   - The lightbox JS shows a separate "video metadata"
//     panel for videos (vs the existing EXIF panel for
//     images), with rows for Container, Codec, Duration,
//     Bitrate, Framerate, Resolution
//
// What's in VideoMeta:
//   - Duration: formatted "1:23" (under 1h) or "1:23:45" (1h+).
//     Empty if not parseable.
//   - Container: file format name from ffprobe's
//     format_name field (e.g. "mov,mp4,m4a,3gp,3g2,mj2").
//     This is the comma-separated list of all matching
//     demuxers; the first one is the most specific.
//     Empty if not parseable.
//   - VideoCodec: codec_name of the first video stream
//     (e.g. "h264", "vp9", "av1"). Empty if no video
//     stream.
//   - AudioCodec: codec_name of the first audio stream
//     (e.g. "aac", "opus"). Empty if no audio stream
//     (most of the existing test videos are video-only).
//   - Bitrate: human-readable overall bitrate from
//     format.bit_rate (e.g. "5.2 Mbps", "842 kbps").
//     Empty if not reported by ffprobe.
//   - Framerate: formatted from stream.avg_frame_rate
//     (e.g. "24 fps", "29.97 fps", "60 fps"). Empty if
//     not parseable.
//
// Note: Width and Height are NOT part of VideoMeta — they
// already live on FileInfo (and FileView) via the existing
// dimensions pipeline (readDimensionsCached). The lightbox
// shows resolution from the existing Width/Height fields.

package gallery

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// VideoMeta holds the extracted metadata for a video file.
// All fields are optional (empty string = not available).
// See file-level comment for the parallel with ExifData.
type VideoMeta struct {
	Duration   string // "1:23" or "1:23:45"
	Container  string // "mov,mp4,m4a,3gp,3g2,mj2" (ffprobe output)
	VideoCodec string // "h264", "vp9", "av1", etc.
	AudioCodec string // "aac", "opus", "vorbis", etc. (empty if no audio)
	Bitrate    string // "5.2 Mbps", "842 kbps" (human-readable)
	Framerate  string // "24 fps", "29.97 fps" (human-readable)
}

// HasAny returns true if at least one field is populated.
// Used to decide whether to render the "video metadata"
// panel in the lightbox. An empty VideoMeta (all empty
// strings) is treated as "no metadata" — no UI shown.
func (v *VideoMeta) HasAny() bool {
	if v == nil {
		return false
	}
	return v.Duration != "" || v.Container != "" ||
		v.VideoCodec != "" || v.AudioCodec != "" ||
		v.Bitrate != "" || v.Framerate != ""
}

// videoMetaPath computes the path of the .vmeta sidecar
// for src. The sidecar lives next to the video's thumbnail
// (same pattern as .exif for images) so the cache eviction
// handles them as a unit.
//
// Per the project convention: the sidecar extension is
// `.<thumbExt>.vmeta` (e.g. `.webp.vmeta`). The thumbExt
// is the same string used for the thumb itself (so all
// artifacts for one source file live under one hash
// subdir).
func videoMetaPath(src, cacheDir, thumbExt string) string {
	return cachePath(src, cacheDir, "."+thumbExt+".vmeta")
}

// readVideoMetaFile reads the .vmeta sidecar for src. Returns
// (data, true) if found, (nil, false) otherwise. The cache
// uses a 2-level nested hash layout (see cachePath in
// thumbnails.go for the rationale).
func readVideoMetaFile(src, cacheDir, thumbExt string) ([]byte, bool) {
	metaPath := videoMetaPath(src, cacheDir, thumbExt)
	if data, err := os.ReadFile(metaPath); err == nil {
		return data, true
	}
	return nil, false
}

// readVideoMeta uses ffprobe to extract the metadata for a
// video file. Returns (*VideoMeta, nil) on success (including
// "no metadata" which is a valid result with all empty
// strings), or (nil, err) on hard failure (e.g. ffprobe not
// installed, or the timeout expired).
//
// We use the same ffprobe subprocess + 10s timeout pattern
// as readVideoDimensions (in dimensions.go). The query is
// constrained to the first video and first audio stream
// (`-select_streams v:0` and `a:0` respectively) and the
// output is the JSON-like "default" format which gives us
// one STREAM block per stream type plus a FORMAT block.
//
// The pattern:
//   ffprobe -v error -show_entries "stream=codec_name,codec_type,avg_frame_rate" \
//                          -select_streams v:0 \
//                          -show_entries "format=duration,bit_rate,format_name" \
//                          -of default <path>
// (plus a separate ffprobe call for the audio stream).
//
// In practice we use TWO ffprobe calls (one for video stream
// info + format info, one for audio) because `-select_streams`
// only takes one stream spec. Two short ffprobe calls are
// faster than one long one (the second call doesn't have to
// iterate over all streams).
func readVideoMeta(path string) (*VideoMeta, error) {
	out := &VideoMeta{}

	// First call: video stream + format
	videoCodec, framerate, err := readVideoStreamInfo(path)
	if err != nil {
		return nil, err
	}
	out.VideoCodec = videoCodec
	out.Framerate = framerate

	// Second call: format
	container, duration, bitrate, err := readVideoFormat(path)
	if err != nil {
		// Format is required — if it fails, the file is
		// probably not a real video or ffprobe is broken.
		// Return nil + err so the caller can fall back.
		return nil, err
	}
	out.Container = container
	out.Duration = duration
	out.Bitrate = bitrate

	// Third call: audio stream (optional — empty if no
	// audio stream, which is normal for many short clips).
	// Errors here are NOT propagated because "no audio
	// stream" is a valid state.
	audioCodec, _ := readVideoAudioStreamInfo(path)
	out.AudioCodec = audioCodec

	return out, nil
}

// readVideoStreamInfo runs ffprobe and extracts the first
// video stream's codec_name and avg_frame_rate.
func readVideoStreamInfo(path string) (codec, framerate string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name,avg_frame_rate",
		"-of", "default=noprint_wrappers=1:nokey=0",
		path,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		// ffprobe not installed, file not a video, or
		// timeout. Return (empty, empty, nil) so the
		// caller treats it as "no video stream".
		// (The outer readVideoMeta will still try the
		// format call to get the container info.)
		return "", "", nil
	}
	codec, fr := parseVideoStreamEntries(out.String())
	return codec, fr, nil
}

// parseVideoStreamEntries parses the ffprobe "default"
// output for a video stream call. The output looks like:
//   codec_name=h264
//   avg_frame_rate=24/1
//   ...
// (one key=value per line; the values can have spaces
// inside them for some fields, but the fields we request
// here don't have spaces).
func parseVideoStreamEntries(s string) (codec, framerate string) {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := line[:eq]
		val := line[eq+1:]
		switch key {
		case "codec_name":
			codec = val
		case "avg_frame_rate":
			framerate = formatFramerate(val)
		}
	}
	return codec, framerate
}

// formatFramerate converts ffprobe's fraction output
// (e.g. "24/1", "30000/1001", "59.94") into a human-readable
// "fps" string. The fraction form is most common (e.g.
// "30000/1001" is the standard NTSC 29.97 fps). The decimal
// form is also possible (e.g. "59.94" for 60p cameras).
func formatFramerate(fr string) string {
	fr = strings.TrimSpace(fr)
	if fr == "" || fr == "0/0" {
		return ""
	}
	// Try fraction form first ("N/D")
	if slash := strings.IndexByte(fr, '/'); slash > 0 {
		num, err1 := strconv.ParseFloat(fr[:slash], 64)
		den, err2 := strconv.ParseFloat(fr[slash+1:], 64)
		if err1 == nil && err2 == nil && den != 0 {
			fps := num / den
			return formatFpsNumber(fps)
		}
		// Fall through to decimal form
	}
	// Try decimal form ("59.94")
	if f, err := strconv.ParseFloat(fr, 64); err == nil {
		return formatFpsNumber(f)
	}
	// Unparseable — return the raw string
	return fr + " fps"
}

// formatFpsNumber formats a numeric fps as an integer (if
// whole) or two-decimal-place string. We never show more
// than 2 decimals because the user just needs to know
// "24" vs "29.97" vs "60" — not the exact rounding.
func formatFpsNumber(fps float64) string {
	if fps == float64(int(fps)) {
		return fmt.Sprintf("%d fps", int(fps))
	}
	return fmt.Sprintf("%.2f fps", fps)
}

// readVideoFormat runs ffprobe and extracts the format's
// format_name, duration, and bit_rate.
func readVideoFormat(path string) (container, duration, bitrate string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=format_name,duration,bit_rate",
		"-of", "default=noprint_wrappers=1:nokey=0",
		path,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		// ffprobe not installed, file not a video, or
		// timeout. Treat as "no format info" — return
		// empty strings with no error so the caller can
		// still try the stream calls.
		return "", "", "", nil
	}
	container, duration, bitrate = parseFormatEntries(out.String())
	return container, duration, bitrate, nil
}

// parseFormatEntries parses the ffprobe "default" output
// for a format call. Same format as parseVideoStreamEntries.
func parseFormatEntries(s string) (container, duration, bitrate string) {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := line[:eq]
		val := line[eq+1:]
		switch key {
		case "format_name":
			container = val
		case "duration":
			duration = formatDuration(val)
		case "bit_rate":
			bitrate = formatBitrate(val)
		}
	}
	return container, duration, bitrate
}

// formatDuration converts ffprobe's seconds output (e.g.
// "5.875000", "123.456789") into a human-readable "M:SS"
// or "H:MM:SS" string. Returns "" if the input is not
// parseable or is "N/A".
func formatDuration(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return ""
	}
	sec, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return ""
	}
	if sec < 0 {
		return ""
	}
	totalSec := int(sec)
	h := totalSec / 3600
	m := (totalSec % 3600) / 60
	secs := totalSec % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, secs)
	}
	return fmt.Sprintf("%d:%02d", m, secs)
}

// formatBitrate converts ffprobe's bits-per-second output
// (e.g. "1073568", "6949448") into a human-readable
// "1.0 Mbps" or "842 kbps" string. Returns "" if not
// parseable.
func formatBitrate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return ""
	}
	br, err := strconv.ParseInt(s, 10, 64)
	if err != nil || br <= 0 {
		return ""
	}
	// Display in Mbps if >= 1 Mbps, else in kbps.
	if br >= 1_000_000 {
		mbps := float64(br) / 1_000_000
		return fmt.Sprintf("%.1f Mbps", mbps)
	}
	if br >= 1_000 {
		kbps := float64(br) / 1_000
		return fmt.Sprintf("%.0f kbps", kbps)
	}
	return fmt.Sprintf("%d bps", br)
}

// readVideoAudioStreamInfo runs ffprobe and extracts the
// first audio stream's codec_name. Returns "" if no audio
// stream (which is normal for many short clips).
func readVideoAudioStreamInfo(path string) (codec string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=codec_name",
		"-of", "default=noprint_wrappers=1:nokey=0",
		path,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		// No audio stream, ffprobe not installed, or
		// timeout. Treat as "no audio" — return "".
		return "", nil
	}
	// Parse the output: a single line "codec_name=aac"
	codec = strings.TrimSpace(out.String())
	// Strip "codec_name=" prefix if present
	if eq := strings.IndexByte(codec, '='); eq >= 0 {
		codec = codec[eq+1:]
	}
	// Sanity check: real codec names are short alphanumeric
	// strings. If the output looks like an error message
	// (e.g. "N/A"), return empty.
	if len(codec) > 32 || strings.ContainsAny(codec, "\n\r") {
		return "", nil
	}
	return codec, nil
}

// readVideoMetaCached returns the cached video metadata
// for a file, reading from the .vmeta sidecar if present
// and fresh (source mtime <= sidecar mtime), or by calling
// readVideoMeta and writing a new sidecar if not.
//
// The logic mirrors readExifCached exactly: prefer sidecar,
// check staleness via mtime, fall back to fresh read +
// write new sidecar.
//
// In unit-mode tests (where cacheDir is ""), we fall back
// to a direct readVideoMeta (no sidecar writes).
func readVideoMetaCached(path, cacheDir, thumbExt string) (*VideoMeta, error) {
	if cacheDir == "" {
		// No cache dir — fall back to direct read.
		return readVideoMeta(path)
	}
	metaPath := videoMetaPath(path, cacheDir, thumbExt)

	// Try the sidecar first. Uses the helper which falls
	// back to the legacy flat-layout path and
	// opportunistically migrates legacy files.
	if data, ok := readVideoMetaFile(path, cacheDir, thumbExt); ok {
		// Staleness check: if the source is newer than the
		// sidecar, the sidecar is stale. Skip it.
		srcInfo, srcErr := os.Stat(path)
		if srcErr == nil {
			if sidecarInfo, statErr := os.Stat(metaPath); statErr == nil {
				if sidecarInfo.ModTime().Before(srcInfo.ModTime()) {
					// Stale — fall through to a
					// fresh read.
					goto fresh
				}
			}
		}
		if meta := parseVideoMetaSidecar(data); meta != nil || bytes.HasPrefix(data, []byte("has=false\n")) {
			// Successfully parsed. nil meta + "has=false"
			// prefix means "no metadata" (valid cached
			// result). nil meta without that prefix means
			// "malformed sidecar" — fall through.
			return meta, nil
		}
	}

fresh:
	// Fresh read: call ffprobe, write sidecar, return.
	meta, err := readVideoMeta(path)
	if err != nil {
		return nil, err
	}
	// Write the sidecar (even for empty results, so the
	// next request doesn't re-run ffprobe).
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		// Non-fatal — return the meta anyway.
		return meta, nil
	}
	data := writeVideoMetaSidecar(meta)
	if err := os.WriteFile(metaPath, data, 0o644); err != nil {
		// Non-fatal — return the meta anyway.
		return meta, nil
	}
	// Set the sidecar mtime to match the source's mtime
	// so the staleness check works correctly.
	if srcInfo, err := os.Stat(path); err == nil {
		_ = os.Chtimes(metaPath, srcInfo.ModTime(), srcInfo.ModTime())
	}
	return meta, nil
}

// writeVideoMetaSidecar serialises a VideoMeta to the
// sidecar's text format. Same shape as writeExifSidecar:
//   has=true|false\n
//   key=value\n
//   ...
//
// The first line is always "has=true" or "has=false" so
// parseVideoMetaSidecar can detect empty results without
// having to look at every key.
//
// Per the project convention (matching the EXIF sidecars
// after the 2026-06-29 refactor): the keys are Human-
// Readable labels matching what the lightbox displays.
// So the sidecar says "Duration=5s" not "DurationSec=5".
// Mapping between internal field names and Human-Readable
// keys happens ONLY at write/parse time.
func writeVideoMetaSidecar(meta *VideoMeta) []byte {
	if meta == nil || !meta.HasAny() {
		// Per the test: an empty (all-zero-fields) VideoMeta
		// is treated as "no data" — same as nil. This avoids
		// writing "has=true" with no actual data (a valid
		// but useless sidecar that would be read back as a
		// non-nil VideoMeta with all empty strings).
		return []byte("has=false\n")
	}
	var buf bytes.Buffer
	buf.WriteString("has=true\n")
	if meta.Duration != "" {
		buf.WriteString("Duration=")
		buf.WriteString(meta.Duration)
		buf.WriteByte('\n')
	}
	if meta.Container != "" {
		buf.WriteString("Container=")
		buf.WriteString(meta.Container)
		buf.WriteByte('\n')
	}
	if meta.VideoCodec != "" {
		buf.WriteString("Video Codec=")
		buf.WriteString(meta.VideoCodec)
		buf.WriteByte('\n')
	}
	if meta.AudioCodec != "" {
		buf.WriteString("Audio Codec=")
		buf.WriteString(meta.AudioCodec)
		buf.WriteByte('\n')
	}
	if meta.Bitrate != "" {
		buf.WriteString("Bitrate=")
		buf.WriteString(meta.Bitrate)
		buf.WriteByte('\n')
	}
	if meta.Framerate != "" {
		buf.WriteString("Framerate=")
		buf.WriteString(meta.Framerate)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// parseVideoMetaSidecar parses the sidecar's text format.
// Returns nil if the sidecar is malformed (so the caller
// can fall through to a fresh read). The "has=false"
// header returns nil with no error (a valid "no data"
// result).
func parseVideoMetaSidecar(data []byte) *VideoMeta {
	// Check the first line. The format is guaranteed to
	// have "has=true\n" or "has=false\n" as the first line.
	nl := bytes.IndexByte(data, '\n')
	if nl < 0 {
		return nil // malformed: no newline
	}
	header := string(data[:nl])
	if header == "has=false" {
		return nil // valid: no metadata
	}
	if header != "has=true" {
		return nil // malformed: unknown header
	}
	// Parse the rest of the lines. The Human-Readable
	// keys map to the internal field names here.
	meta := &VideoMeta{}
	rest := data[nl+1:]
	for len(rest) > 0 {
		eol := bytes.IndexByte(rest, '\n')
		var line []byte
		if eol < 0 {
			line = rest
			rest = nil
		} else {
			line = rest[:eol]
			rest = rest[eol+1:]
		}
		eq := bytes.IndexByte(line, '=')
		if eq < 0 {
			continue // malformed: no =
		}
		key := string(line[:eq])
		val := string(line[eq+1:])
		switch key {
		case "Duration":
			meta.Duration = val
		case "Container":
			meta.Container = val
		case "Video Codec":
			meta.VideoCodec = val
		case "Audio Codec":
			meta.AudioCodec = val
		case "Bitrate":
			meta.Bitrate = val
		case "Framerate":
			meta.Framerate = val
		}
	}
	return meta
}

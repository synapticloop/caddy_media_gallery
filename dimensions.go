package gallery

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "golang.org/x/image/webp" // registers webp decoder
)

// readDimensions returns the pixel dimensions (W × H) of the
// image or video at path. Returns (0, 0, nil) if the dimensions
// cannot be determined (e.g. the file is a non-decodable format
// like AVIF/HEIC/SVG, or the file is corrupted, or it's not an
// image/video at all). Returns (0, 0, err) only for genuinely
// unexpected I/O errors (e.g. file permission denied).
//
// Per user request 2026-06-27: dimensions of the SOURCE file
// (the original image/video the thumbnail was generated from),
// not the thumbnail dimensions.
//
// Supported formats:
//
//   - Images: JPEG, PNG, GIF, WebP (via image.DecodeConfig +
//     golang.org/x/image/webp for WebP). Note: DecodeConfig
//     only reads the image header — it does NOT decode the
//     full pixel data, so it's fast (1-5ms per file).
//   - Videos: MP4, WebM, MOV, MKV, AVI, OGV, OGG, M4V (via
//     ffprobe subprocess, ~50-100ms per file)
//
// NOT supported: SVG (per user request), AVIF, HEIC (no Go
// libraries, no ffmpeg fallback).
func readDimensions(path string) (width, height int, err error) {
	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return readImageDimensions(path)
	case ".mp4", ".webm", ".mov", ".mkv", ".avi", ".ogv", ".ogg", ".m4v":
		return readVideoDimensions(path)
	default:
		// Not an image or video (e.g. text file, HTML).
		// Not an error — just no dimensions to report.
		return 0, 0, nil
	}
}

// readImageDimensions uses image.DecodeConfig to read only
// the image header. Returns (0, 0, nil) for non-image files
// or files that can't be decoded (corrupted, unsupported
// format like AVIF/HEIC).
func readImageDimensions(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("readImageDimensions: open: %w", err)
	}
	defer f.Close()

	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		// DecodeConfig returns an error for files it can't
		// recognise (AVIF, HEIC, SVG, random binary data, etc.).
		// We treat these as "no dimensions" rather than a fatal
		// error so the scan continues for the rest of the
		// directory.
		return 0, 0, nil
	}
	return cfg.Width, cfg.Height, nil
}

// readVideoDimensions uses ffprobe to read the first video
// stream's width/height. ffprobe is part of the ffmpeg
// toolchain (already installed for video thumbnail generation).
//
// The query is constrained to the first video stream
// (`-select_streams v:0`) and outputs just width,height as
// CSV (`-of csv=p=0`). The output looks like "1920,1080" or
// sometimes empty if the file has no video stream.
//
// We wrap the call in a 10-second timeout so a broken ffprobe
// can't hang the scan.
func readVideoDimensions(path string) (int, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-of", "csv=p=0",
		path,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil // discard; ffprobe is noisy on errors

	if err := cmd.Run(); err != nil {
		// ffprobe not installed, file not a video, or
		// timeout — treat as "no dimensions".
		return 0, 0, nil
	}

	// Output is "1920,1080\n" (or empty for audio-only).
	output := strings.TrimSpace(out.String())
	if output == "" {
		return 0, 0, nil
	}

	parts := strings.Split(output, ",")
	if len(parts) != 2 {
		return 0, 0, nil
	}

	var w, h int
	if _, err := fmt.Sscanf(parts[0], "%d", &w); err != nil {
		return 0, 0, nil
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &h); err != nil {
		return 0, 0, nil
	}
	if w <= 0 || h <= 0 {
		return 0, 0, nil
	}
	return w, h, nil
}

// formatDimensions returns a human-readable "WIDTH × HEIGHT"
// string for the watermark display. Returns "" if either
// dimension is zero (caller can use this to skip rendering).
func formatDimensions(width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	return fmt.Sprintf("%d × %d", width, height)
}

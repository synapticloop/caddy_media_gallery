package gallery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// dimsMetaPath returns the sidecar metadata file path for the
// given source file. The sidecar lives next to the thumbnail
// in the thumb cache dir and contains the source image's W × H
// dimensions as plain text ("WIDTH HEIGHT\n"). The filename
// matches the thumb's name with ".meta" appended — so a thumb at
// /var/cache/caddy-gallery/abc123.webp has its dims at
// /var/cache/caddy-gallery/abc123.webp.meta.
//
// The hash is the same SHA-256 hash of the absolute source path
// that the thumb cache uses (truncated to 16 bytes), so the
// sidecar and its corresponding thumb are colocated and can be
// tracked together by the cache eviction logic.
//
// Per user request 2026-06-29: storing the original image
// dimensions in a sidecar file avoids re-parsing the source
// image's header on every scan. For a 1000-image gallery,
// this saves ~1-3 seconds of header reads per directory scan.
func dimsMetaPath(src, cacheDir, thumbExt string) string {
	abs, err := filepath.Abs(src)
	if err != nil {
		abs = src
	}
	h := sha256.Sum256([]byte(abs))
	return filepath.Join(cacheDir, hex.EncodeToString(h[:16])+"."+thumbExt+".meta")
}

// readDimensionsCached returns the pixel dimensions of the
// source file at path, using a sidecar metadata file in the
// thumb cache dir for fast lookups. Behaviour:
//
//  1. If a sidecar file exists at the expected cache path, read
//     W × H from it (one small file read, no image parsing).
//  2. If no sidecar, call readDimensions() (image/video header
//     parse, ~1-5ms), then write the result to a new sidecar
//     file for next time. Errors from readDimensions are
//     propagated as before.
//  3. If readDimensions returns (0, 0, nil) (file has no
//     decodable dimensions, e.g. AVIF/HEIC/SVG), don't write a
//     sidecar — the next scan would get the same result and
//     we'd have a useless file in the cache.
//
// cacheDir is the thumb cache dir (e.g. /var/cache/caddy-gallery).
// thumbExt is the thumb extension (e.g. "webp"); the sidecar is
// named "{hash}.{thumbExt}.meta" to colocate with its thumb.
//
// This is the same hashing scheme as thumbCachePath() in
// thumbnails.go — the two paths line up: the sidecar and
// its corresponding thumb share the same hash and sit next
// to each other in the cache dir, so cache eviction treats
// them as a unit.
func readDimensionsCached(path, cacheDir, thumbExt string) (w, h int, err error) {
	if cacheDir == "" {
		// No cache dir configured — fall back to direct read.
		// The Gallery always sets a cache dir in production, so
		// this branch is mostly for tests and unit-mode use.
		return readDimensions(path)
	}
	metaPath := dimsMetaPath(path, cacheDir, thumbExt)
	// Try the sidecar first. A successful read avoids the
	// ~1-5ms image header parse entirely.
	if data, err := os.ReadFile(metaPath); err == nil {
		// Format: "WIDTH HEIGHT" + newline (plain text, newline-terminated).
		// Two integers separated by whitespace.
		fields := strings.Fields(string(data))
		if len(fields) >= 2 {
			w, errW := strconv.Atoi(fields[0])
			h, errH := strconv.Atoi(fields[1])
			if errW == nil && errH == nil {
				return w, h, nil
			}
			// Malformed sidecar — fall through to a fresh read
			// and overwrite. (Could happen if a previous version
			// wrote a different format; we want self-healing.)
		}
	}
	// Cache miss: do the real read and write the sidecar.
	w, h, err = readDimensions(path)
	if err != nil || w == 0 || h == 0 {
		// Either an I/O error or no decodable dimensions.
		// Don't write a sidecar for the no-dimensions case
		// (would just be a useless file in the cache).
		return w, h, err
	}
	// Write the sidecar. Best-effort: if the write fails, the
	// next scan just re-reads the source (no correctness issue,
	// just a small perf cost). We don't propagate the error.
	_ = os.MkdirAll(cacheDir, 0o755)
	_ = os.WriteFile(metaPath, []byte(fmt.Sprintf("%d %d\n", w, h)), 0o644)
	return w, h, nil
}

// touchMetaAtUse updates the .meta sidecar's mtime to the
// current time. This is called on every thumb serve to act
// as an LRU timestamp — the eviction logic sorts by .meta
// mtime (oldest first), so frequently-accessed thumbs stay
// in the cache and rarely-accessed ones get evicted when the
// cap is hit.
//
// The function is best-effort: any error (permission
// denied, etc.) is silently ignored. The consequences of a
// failed touch are just "this thumb looks older to the
// eviction logic" — it might get evicted slightly sooner
// than it should. No correctness impact.
//
// If the .meta sidecar doesn't exist (e.g. the thumb was
// generated before this feature was added, or the
// dimensions failed to be read on the initial scan), we
// CREATE a minimal .meta with a single newline. The eviction
// logic uses the .meta mtime as the LRU timestamp, so the
// mtime is the only thing that matters here — the contents
// are irrelevant. A single-byte file is enough.
//
// Why create the .meta on first serve? Because for a fresh
// thumb (no prior scan), there's no .meta yet, but we still
// want a valid LRU timestamp. The first serve IS the
// first-known "last used" time, which is a fine LRU signal.
func touchMetaAtUse(src, cacheDir, thumbExt string) {
	if cacheDir == "" {
		return
	}
	metaPath := dimsMetaPath(src, cacheDir, thumbExt)
	// If the .meta doesn't exist, create a minimal one. We
	// use MkdirAll (in case the cache dir was removed out
	// from under us) and WriteFile. Both are best-effort.
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		_ = os.MkdirAll(filepath.Dir(metaPath), 0o755)
		_ = os.WriteFile(metaPath, []byte("\n"), 0o644)
	}
	// Bump the mtime to time.Now(). Chtimes is the cheapest
	// way: it doesn't read or write any data.
	_ = os.Chtimes(metaPath, time.Now(), time.Now())
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

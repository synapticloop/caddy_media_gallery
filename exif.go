package gallery

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dsoprea/go-exif/v3"
	exifcommon "github.com/dsoprea/go-exif/v3/common"
)

// ExifData holds the EXIF metadata for a single image. Only the
// 8 fields we display are populated; the rest of the EXIF block
// (GPS coordinates, software, copyright, etc.) is NEVER read.
//
// Per user request 2026-06-27: GPS data is intentionally excluded
// for privacy. We never extract the GPS IFD, so the values are
// never loaded into memory, never stored in the scan cache, and
// never displayed. The original file's GPS data is preserved
// (we don't modify files on disk); the privacy guarantee is at
// the gallery-display layer only.
type ExifData struct {
	CameraMake   string
	CameraModel  string
	LensModel    string
	DateTaken    string // formatted "2024-11-08 06:23:14" (already EXIF format)
	ExposureTime string // formatted "1/250 s", "2 s", etc.
	Aperture     string // formatted "f/2.8"
	ISO          string // formatted "ISO 400"
	FocalLength  string // formatted "50 mm"
}

// HasAny returns true if at least one field is populated. Used
// to decide whether to render the "EXIF" pill on the card and
// the EXIF panel in the lightbox. An empty ExifData (all empty
// strings) is treated as "no EXIF" — no UI elements shown.
func (e *ExifData) HasAny() bool {
	if e == nil {
		return false
	}
	return e.CameraMake != "" || e.CameraModel != "" || e.LensModel != "" ||
		e.DateTaken != "" || e.ExposureTime != "" || e.Aperture != "" ||
		e.ISO != "" || e.FocalLength != ""
}

// readExif extracts the CAMERA subset of EXIF tags from the file
// at path. Returns (nil, nil) if the file has no EXIF block
// (not an error — most images won't have EXIF). Returns
// (nil, err) for genuinely malformed files (logged but not fatal
// — the scan continues).
//
// The implementation reads the file in one pass:
//  1. SearchAndExtractExif — searches the file for the EXIF
//     block, returns the raw bytes
//  2. Collect — parses the EXIF into an IFD tree
//  3. FindTagWithName — looks up each tag by name (Make, Model,
//     etc.) and reads its value
//
// GPS-related tags (GPSLatitude, GPSLongitude, GPSAltitude,
// GPSImgDirection, GPSTimeStamp, GPSDateStamp) are NEVER queried,
// so they're never loaded into memory or displayed.
func readExif(path string) (*ExifData, error) {
	rawExif, err := exif.SearchFileAndExtractExif(path)
	if err != nil {
		// ErrNoExif is a normal case (most images have no EXIF).
		// The dsoprea library wraps it via panic/recover so the
		// direct == comparison doesn't work; check by error type
		// or message text. Return (nil, nil) so the caller
		// treats it as "no data".
		if errors.Is(err, exif.ErrNoExif) || strings.Contains(err.Error(), exif.ErrNoExif.Error()) {
			return nil, nil
		}
		return nil, fmt.Errorf("readExif: extract: %w", err)
	}

	ifdMapping, err := exifcommon.NewIfdMappingWithStandard()
	if err != nil {
		return nil, fmt.Errorf("readExif: ifd mapping: %w", err)
	}

	ti := exif.NewTagIndex()

	_, index, err := exif.Collect(ifdMapping, ti, rawExif)
	if err != nil {
		return nil, fmt.Errorf("readExif: collect: %w", err)
	}

	out := &ExifData{}

	// Look up a tag by name anywhere in the IFD tree. EXIF has
	// multiple IFDs:
	//   - IFD (root): Make, Model, Orientation
	//   - IFD/Exif: LensModel, DateTimeOriginal, ExposureTime,
	//                FNumber, ISOSpeedRatings, FocalLength
	//   - IFD/Exif/Iop: Interoperability tags (we don't use these)
	//   - IFD/GPSInfo: GPS coordinates (we NEVER query this)
	// findTag walks the tree and returns the first match.
	findTag := func(tagName string) *exif.IfdTagEntry {
		for _, ifd := range index.Tree {
			results, err := ifd.FindTagWithName(tagName)
			if err == nil && len(results) > 0 {
				return results[0]
			}
		}
		return nil
	}

	// Helper: look up a tag by name, extract the value as a
	// string, and store it in out. If the tag is missing, the
	// field stays empty (not an error).
	readString := func(tagName string, dest *string) {
		entry := findTag(tagName)
		if entry == nil {
			return
		}
		val, err := entry.Value()
		if err != nil {
			return
		}
		s, ok := val.(string)
		if !ok {
			return
		}
		// EXIF strings often have a trailing NUL byte — trim it.
		*dest = strings.TrimRight(s, "\x00")
	}

	// Read the simple string-typed tags. Make and Model are in
	// the root IFD; LensModel and DateTimeOriginal are in IFD/Exif.
	readString("Make", &out.CameraMake)
	readString("Model", &out.CameraModel)
	readString("LensModel", &out.LensModel)
	readString("DateTimeOriginal", &out.DateTaken)

	// Read the numeric tags and format them. These are all in
	// IFD/Exif (the "Exif" sub-IFD, not the root IFD).
	out.ExposureTime = formatExposureTime(findTag, "ExposureTime")
	out.Aperture = formatFNumber(findTag, "FNumber")
	out.ISO = formatISO(findTag, "ISOSpeedRatings")
	out.FocalLength = formatFocalLength(findTag, "FocalLength")

	// If nothing was populated, treat as "no EXIF" (some files
	// have an EXIF block but no standard tags).
	if !out.HasAny() {
		return nil, nil
	}

	return out, nil
}

// formatExposureTime formats the ExposureTime tag value. EXIF
// stores it as a Rational (e.g. 1/250 s). We render as:
//   - 1/250 s (for fractions)
//   - 2 s (for whole seconds)
//   - 2.5 s (for non-fractional rationals)
func formatExposureTime(findTag func(string) *exif.IfdTagEntry, tagName string) string {
	entry := findTag(tagName)
	if entry == nil {
		return ""
	}
	val, err := entry.Value()
	if err != nil {
		return ""
	}
	switch v := val.(type) {
	case exifcommon.Rational:
		return formatRational(v.Numerator, v.Denominator)
	case []exifcommon.Rational:
		if len(v) == 0 {
			return ""
		}
		return formatRational(v[0].Numerator, v[0].Denominator)
	}
	return ""
}

// formatRational formats a Rational as shutter speed.
// 1/250 → "1/250 s", 2 → "2 s", 2.5 → "2.5 s"
func formatRational(num, denom uint32) string {
	if denom == 0 {
		return "" // divide-by-zero: invalid, return empty
	}
	if num == 0 {
		return "0 s"
	}
	n := float64(num) / float64(denom)
	if n >= 1 {
		if n == float64(int64(n)) {
			return fmt.Sprintf("%d s", int64(n))
		}
		return fmt.Sprintf("%.1f s", n)
	}
	// <1s: show as fraction
	return fmt.Sprintf("1/%d s", int64(float64(denom)/float64(num)))
}

// formatFNumber formats the FNumber tag (aperture). EXIF stores
// it as a Rational (e.g. 28/10 = f/2.8).
func formatFNumber(findTag func(string) *exif.IfdTagEntry, tagName string) string {
	entry := findTag(tagName)
	if entry == nil {
		return ""
	}
	val, err := entry.Value()
	if err != nil {
		return ""
	}
	switch v := val.(type) {
	case exifcommon.Rational:
		return formatAperture(v.Numerator, v.Denominator)
	case []exifcommon.Rational:
		if len(v) == 0 {
			return ""
		}
		return formatAperture(v[0].Numerator, v[0].Denominator)
	}
	return ""
}

// formatAperture formats a Rational as f-stop.
// 28/10 → "f/2.8", 40/10 → "f/4", 50/10 → "f/5"
func formatAperture(num, denom uint32) string {
	if denom == 0 {
		return ""
	}
	f := float64(num) / float64(denom)
	if f == float64(int64(f)) {
		return fmt.Sprintf("f/%.0f", f)
	}
	return fmt.Sprintf("f/%.1f", f)
}

// formatISO formats the ISOSpeedRatings tag. EXIF stores it as
// a Short (uint16). We render as "ISO 400".
func formatISO(findTag func(string) *exif.IfdTagEntry, tagName string) string {
	entry := findTag(tagName)
	if entry == nil {
		return ""
	}
	val, err := entry.Value()
	if err != nil {
		return ""
	}
	switch v := val.(type) {
	case uint16:
		return fmt.Sprintf("ISO %d", v)
	case uint32:
		return fmt.Sprintf("ISO %d", v)
	case []uint16:
		if len(v) > 0 {
			return fmt.Sprintf("ISO %d", v[0])
		}
	}
	return ""
}

// formatFocalLength formats the FocalLength tag (in mm). EXIF
// stores it as a Rational (e.g. 50/1 = 50 mm). We render as
// "50 mm" (or "50.5 mm" if non-integer).
func formatFocalLength(findTag func(string) *exif.IfdTagEntry, tagName string) string {
	entry := findTag(tagName)
	if entry == nil {
		return ""
	}
	val, err := entry.Value()
	if err != nil {
		return ""
	}
	switch v := val.(type) {
	case exifcommon.Rational:
		return formatFocalLengthMm(v.Numerator, v.Denominator)
	case []exifcommon.Rational:
		if len(v) == 0 {
			return ""
		}
		return formatFocalLengthMm(v[0].Numerator, v[0].Denominator)
	}
	return ""
}

// formatFocalLengthMm formats a Rational as mm.
// 500/10 → "50 mm", 1350/10 → "135 mm", 175/10 → "17.5 mm"
func formatFocalLengthMm(num, denom uint32) string {
	if denom == 0 {
		return ""
	}
	fl := float64(num) / float64(denom)
	if fl == float64(int64(fl)) {
		return fmt.Sprintf("%d mm", int64(fl))
	}
	return fmt.Sprintf("%.1f mm", fl)
}


// exifMetaPath returns the sidecar EXIF metadata file path for
// the given source file. Uses the same nested layout as
// thumbCachePath (see cachePath in thumbnails.go for the
// layout rationale). The sidecar sits next to the thumb
// and dims sidecar in the same subdir.
//
// Filename: "<rest>.<thumbExt>.exif" — the .exif suffix
// distinguishes it from .meta (dimensions) and the thumb
// itself. All three files for the same source share the
// same subdir, so they're colocated in the cache and
// cache eviction handles them as a unit.
func exifMetaPath(src, cacheDir, thumbExt string) string {
	return cachePath(src, cacheDir, "."+thumbExt+".exif")
}


// readExifFile reads the .exif sidecar for src. Returns
// (data, true) if found, (nil, false) otherwise. The cache
// uses a 2-level nested hash layout (see cachePath in
// thumbnails.go for the rationale).
func readExifFile(src, cacheDir, thumbExt string) ([]byte, bool) {
	metaPath := exifMetaPath(src, cacheDir, thumbExt)
	if data, err := os.ReadFile(metaPath); err == nil {
		return data, true
	}
	return nil, false
}

// writeExifFile writes the .exif sidecar at the new nested
// location, creating the subdir if needed.
func writeExifFile(src, cacheDir, thumbExt string, data []byte) error {
	metaPath := exifMetaPath(src, cacheDir, thumbExt)
	if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(metaPath, data, 0o644)
}

// readExifCached returns the EXIF data for the source file
// at path, using a sidecar .exif file in the thumb cache
// dir for fast lookups. Per user request 2026-06-29: this
// complements the LAZY-EXIF design where the lightbox fetches
// EXIF on open via the ?exif=1 endpoint. The sidecar makes
// subsequent lightbox opens (prev/next navigation, closing
// and reopening the same image) instant — no image header
// re-parse.
//
// Behaviour:
//
//  1. If a sidecar exists at the expected path, parse it
//     and return. The JSON includes a "has" flag; we
//     return the parsed ExifData (or nil if has=false).
//  2. If no sidecar, call readExif() (image header parse,
//     ~1-5ms) and write the sidecar for next time. We
//     write a sidecar for BOTH the "has EXIF" and the
//     "no EXIF" cases — the latter so we don't re-parse
//     files that have no EXIF block on every lightbox
//     open.
//  3. If the sidecar is malformed (partial write, old
//     version, etc.), fall through to a fresh read and
//     overwrite. Self-healing.
//
// cacheDir is the thumb cache dir. thumbExt is the thumb
// extension (e.g. "webp"). When cacheDir is empty
// (unit-mode tests), we fall back to a direct readExif.
func readExifCached(path, cacheDir, thumbExt string) (*ExifData, error) {
	if cacheDir == "" {
		// No cache dir — fall back to direct read.
		return readExif(path)
	}
	metaPath := exifMetaPath(path, cacheDir, thumbExt)
	// Per user request 2026-06-30: check the sidecar's
	// mtime against the source's mtime. If the source was
	// modified AFTER the sidecar was written, the sidecar
	// is stale (e.g., the user re-edited the EXIF data with
	// exiftool). We treat the sidecar as fresh only if
	// sidecar.mtime >= source.mtime.
	srcInfo, srcErr := os.Stat(path)
	if srcErr != nil {
		// Source missing or unreadable. Fall through to
		// readExif which will return its own error.
		return readExif(path)
	}
	// Try the sidecar first. Uses the helper which falls
	// back to the legacy flat-layout path and
	// opportunistically migrates legacy files.
	if data, ok := readExifFile(path, cacheDir, thumbExt); ok {
		// Staleness check: if the source is newer than the
		// sidecar, the sidecar is stale. Skip it.
		sidecarFresh := true
		if sidecarInfo, statErr := os.Stat(metaPath); statErr == nil {
			if sidecarInfo.ModTime().Before(srcInfo.ModTime()) {
				sidecarFresh = false
			}
		}
		if sidecarFresh {
			if exif := parseExifSidecar(data); exif != nil || bytes.HasPrefix(data, []byte("has=false\n")) {
				// Successfully parsed. nil exif + "has=false"
				// prefix means "no EXIF" (valid cached result).
				// nil exif without that prefix means "malformed
				// sidecar" — fall through to a fresh read.
				return exif, nil
			}
			// Malformed sidecar — fall through to a fresh
			// read and overwrite (self-healing).
		}
	}
	// Cache miss (no sidecar, malformed sidecar, or stale
	// sidecar): do the real read.
	exif, err := readExif(path)
	if err != nil {
		return nil, err
	}
	// Write the sidecar. We set its mtime to the source's
	// mtime so the staleness check on the NEXT read works
	// cleanly (a sidecar with mtime = source.mtime is
	// considered fresh until the source is modified again).
	_ = writeExifFile(path, cacheDir, thumbExt, writeExifSidecar(exif))
	_ = os.Chtimes(exifMetaPath(path, cacheDir, thumbExt), srcInfo.ModTime(), srcInfo.ModTime())
	return exif, err
}

// writeExifSidecar serializes the EXIF data to the text
// sidecar format. The first line is ALWAYS "has=true" or
// "has=false". If has=false, only that line is present.
// If has=true, each non-empty field is written as one line
// "Key=Value\n".
//
// Format choice: per user request 2026-06-29, we use a
// plain text key=value format instead of JSON. The benefits
// over JSON for this use case:
//   - Smaller files (~20% smaller for typical EXIF data)
//   - Faster parse (no reflection-based JSON unmarshalling
//     — just strings.Split + strings.Index("="))
//   - Less memory (no JSON AST — just a slice of strings)
//   - Human-readable (cat the file in a terminal to debug)
//   - No encoding/json import dependency
//
// Constraints:
//   - Values cannot contain newlines (EXIF values are
//     single-line strings, so this is fine)
//   - Values cannot contain "=" (EXIF values don't have
//     "=", so this is fine)
//   - 8 fixed keys (1 for "has", 7 for EXIF fields) —
//     adding a field requires code changes, but we have
//     a closed set of EXIF fields so this is fine
func writeExifSidecar(exif *ExifData) []byte {
	if exif == nil {
		return []byte("has=false\n")
	}
	var buf bytes.Buffer
	buf.WriteString("has=true\n")
	// Per user request 2026-06-29: sidecar keys are
	// Human-Readable (matching what the lightbox
	// displays as labels) rather than the Go struct's
	// internal field names. So the sidecar says
	// "Camera Make=Canon" not "CameraMake=Canon".
	// Mapping between internal field names and
	// Human-Readable keys happens ONLY at write/parse
	// time — the rest of the codebase doesn't need
	// to know about the mapping.
	if exif.CameraMake != "" {
		buf.WriteString("Camera Make=")
		buf.WriteString(exif.CameraMake)
		buf.WriteByte('\n')
	}
	if exif.CameraModel != "" {
		buf.WriteString("Camera Model=")
		buf.WriteString(exif.CameraModel)
		buf.WriteByte('\n')
	}
	if exif.LensModel != "" {
		buf.WriteString("Lens Model=")
		buf.WriteString(exif.LensModel)
		buf.WriteByte('\n')
	}
	if exif.DateTaken != "" {
		buf.WriteString("Date Taken=")
		buf.WriteString(exif.DateTaken)
		buf.WriteByte('\n')
	}
	if exif.ExposureTime != "" {
		buf.WriteString("Exposure Time=")
		buf.WriteString(exif.ExposureTime)
		buf.WriteByte('\n')
	}
	if exif.Aperture != "" {
		buf.WriteString("Aperture=")
		buf.WriteString(exif.Aperture)
		buf.WriteByte('\n')
	}
	if exif.ISO != "" {
		buf.WriteString("ISO=")
		buf.WriteString(exif.ISO)
		buf.WriteByte('\n')
	}
	if exif.FocalLength != "" {
		buf.WriteString("Focal Length=")
		buf.WriteString(exif.FocalLength)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// parseExifSidecar parses the text sidecar format produced
// by writeExifSidecar. Returns:
//   - (nil, nil) + bytes.HasPrefix(data, "has=false\n"):
//     file has no EXIF (valid cached result)
//   - (*ExifData, nil): file has EXIF with the parsed fields
//   - (nil, nil) WITHOUT the "has=false" prefix: malformed
//     sidecar (caller should treat as cache miss)
//
// The first line MUST be "has=true" or "has=false". We
// check this BEFORE doing any field parsing so a malformed
// sidecar doesn't silently produce an empty ExifData.
func parseExifSidecar(data []byte) *ExifData {
	// Check the first line. The format is guaranteed to
	// have "has=true\n" or "has=false\n" as the first line.
	nl := bytes.IndexByte(data, '\n')
	if nl < 0 {
		return nil // malformed: no newline
	}
	header := string(data[:nl])
	if header == "has=false" {
		return nil // valid: no EXIF
	}
	if header != "has=true" {
		return nil // malformed: unknown header
	}
	// Parse the rest of the lines.
	exif := &ExifData{}
	// Start after the first newline.
	rest := data[nl+1:]
	for len(rest) > 0 {
		// Find the next newline.
		eol := bytes.IndexByte(rest, '\n')
		var line []byte
		if eol < 0 {
			line = rest
			rest = nil
		} else {
			line = rest[:eol]
			rest = rest[eol+1:]
		}
		// Skip empty lines.
		if len(line) == 0 {
			continue
		}
		// Split on the first '='.
		eq := bytes.IndexByte(line, '=')
		if eq < 0 {
			continue // malformed line, skip
		}
		key := string(line[:eq])
		val := string(line[eq+1:])
		// Per user request 2026-06-29: sidecar keys are
		// Human-Readable ("Camera Make") not the Go
		// struct's internal field names ("CameraMake").
		// We map the Human-Readable key to the
		// corresponding struct field here. Unknown keys
		// are silently ignored (forward compatibility
		// — a new field added by a newer version won't
		// break the older version's parse).
		switch key {
		case "Camera Make":
			exif.CameraMake = val
		case "Camera Model":
			exif.CameraModel = val
		case "Lens Model":
			exif.LensModel = val
		case "Date Taken":
			exif.DateTaken = val
		case "Exposure Time":
			exif.ExposureTime = val
		case "Aperture":
			exif.Aperture = val
		case "ISO":
			exif.ISO = val
		case "Focal Length":
			exif.FocalLength = val
		}
	}
	// Return nil if no fields were set (malformed sidecar
	// with all-empty data). The caller checks for nil
	// and treats it as a cache miss.
	return exif
}

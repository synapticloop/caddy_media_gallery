package gallery

import (
	"errors"
	"fmt"
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

package gallery

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FileKind categorises an entry in a gallery directory.
type FileKind string

const (
	// KindDir is a subdirectory. The Name is the directory basename
	// (not the full path); the scanner joins it to Root at render
	// time.
	KindDir FileKind = "dir"
	// KindImage is an image file (jpg, jpeg, png, gif, webp — the formats
// the default image_types list supports). HEIC, AVIF, and SVG are
// NOT classified as KindImage by default — they appear in the
// "Other files" section.
	KindImage FileKind = "image"
	// KindVideo is a video file (mp4, webm).
	KindVideo FileKind = "video"
	// KindOther is a non-image, non-video file (html, txt, etc.).
	// These are shown in the "Other files" strip above the image
	// grid.
	KindOther FileKind = "other"
)

// Default image and video extension sets. Used when the
// operator has not customised them via the Caddyfile
// (`media_gallery { image_types ... video_types ... }`).
//
// Each set is a map of lowercased extension (including the
// leading dot) → true. Lookup in Classify() is case-insensitive
// because the scanner lowercases the file's extension before
// the lookup.
// Per user request 2026-06-30: HEIC, AVIF, and SVG are
// NOT in the default image types list. Go's stdlib doesn't
// decode these formats, so the gallery would show broken
// image icons for them. The scanner still classifies them
// as KindOther (via the fallback in Classify), so they
// appear in the "Other files" section with a 📄 icon.
// Operators can still add them via `image_types .heic
// .avif .svg` if they want to handle these formats with
// external tooling, but the default is now only the formats
// the thumbnail generator actually supports.
var defaultImageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true,
	".gif": true, ".webp": true,
}

var defaultVideoExts = map[string]bool{
	".mp4": true, ".webm": true,
	".m4v": true, ".mov": true, ".mkv": true,
	".avi": true, ".ogv": true, ".ogg": true,
}

// extsToMap is a small helper used by Provision() to convert
// a Caddyfile list (e.g. "jpg jpeg png") into a map for
// Classify() to look up in. Each entry is lowercased and
// dotted before insertion ("jpg" → ".jpg"). Empty entries
// (e.g. a stray double-space) are silently skipped.
func extsToMap(list []string) map[string]bool {
	out := make(map[string]bool, len(list))
	for _, e := range list {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		out[strings.ToLower(e)] = true
	}
	return out
}

// FileInfo describes a single entry in a gallery directory. This is
// the type that flows into the template renderer.
//
// ModTime is unix nanoseconds. int64 keeps the type JSON-friendly
// (no time.Time marshalling quirks) and nanosecond resolution
// preserves sub-second ordering of files written close together.
type FileInfo struct {
	Name    string   `json:"name"`
	ModTime int64    `json:"mtime"`
	Size    int64    `json:"size"`
	Kind    FileKind `json:"kind"`
	// Width and Height are the pixel dimensions of the source
	// image or video (the file the thumbnail was generated
	// from). Zero means "dimensions not available" — for
	// unsupported formats (AVIF, HEIC, SVG) the scanner
	// skips readDimensions. Per user request 2026-06-27:
	// dimensions are read at scan time and cached alongside
	// the rest of the file metadata.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	// CountItems is the number of NON-directory entries
	// (files, symlinks-to-files, broken symlinks, etc.) inside
	// this subdirectory. Only meaningful for KindDir entries.
	// Populated by Scanner.Scan; the count is computed by
	// reading the subdir's contents (one extra os.ReadDir
	// syscall per subdir, then discarded).
	// Exif is the CAMERA-subset EXIF metadata for this file
	// (only meaningful for image files). Per user request
	// 2026-06-29: EXIF is read LAZILY (on lightbox open via
	// the ?exif=1 endpoint), NOT at scan time. This field
	// is always nil from the scanner — it's only set by
	// the EXIF endpoint handler when the lightbox asks.
	// Keeping the field for backward compatibility with
	// any code that might check it; the rendered HTML
	// no longer references FileInfo.Exif.
	//
	// The legacy CAMERA subset (Make, Model, Lens,
	// DateTimeOriginal, ExposureTime, FNumber,
	// ISOSpeedRatings, FocalLength) is preserved; GPS is
	// never extracted. See exif.go for details.
	Exif *ExifData `json:"exif,omitempty"`
	CountItems int       `json:"count_items"`
	// CountDirs is the number of directories inside this
	// subdirectory. Includes real directories AND symlinks
	// that point to directories (per user request 2026-06-27).
	// Only meaningful for KindDir entries.
	CountDirs int `json:"count_dirs"`
}

// Scanner reads a directory and produces a sorted []FileInfo.
// Both directories and files are included; the Kind field tells
// them apart.
type Scanner struct {
	Root      string
	Sort      string // "mtime" (default) or "name"
	ImageExts map[string]bool
	VideoExts map[string]bool
	// NoExif, when true, disables the readExif call for
	// image files (no I/O, no parsing). Set by the
	// Gallery's no_exif Caddyfile directive; passed
	// through ScanCache.Get to the per-directory Scanner.
	// When false (the default), EXIF is read eagerly at
	// scan time. See gallery.go for the full rationale.
	NoExif bool
	// ThumbCacheDir is the on-disk thumb cache dir. When set
	// (always set in production, via thumbCacheDir() in
	// gallery.go), the scanner uses readDimensionsCached to
	// avoid re-parsing source image headers on every scan.
	// The source dimensions are stored in a sidecar file
	// alongside the thumb. See readDimensionsCached in
	// dimensions.go for the cache file format.
	ThumbCacheDir string
	// ThumbFormat is the thumb file extension (e.g. "webp").
	// Used to derive the sidecar path. Defaults to "webp" if
	// empty (so unit-mode tests that don't set up the full
	// cache dir still work).
	ThumbFormat string
}

// NewScanner returns a Scanner for the given root directory with
// default sort order (mtime desc — newest first) and the default
// image / video extension sets.
func NewScanner(root string) *Scanner {
	return &Scanner{
		Root:      root,
		Sort:      "mtime",
		ImageExts: defaultImageExts,
		VideoExts: defaultVideoExts,
	}
}

// Classify returns the FileKind for a filename based on its
// extension. Directories are not classified by name; the scanner
// uses the entry's IsDir() to set KindDir directly.
//
// The image and video extension sets come from the Gallery
// (operator-configurable via the Caddyfile). If neither set is
// provided, the defaults are used (jpg/jpeg/png/gif/webp
// for images; mp4/webm/m4v/mov/mkv/avi/ogv/ogg for videos).
func Classify(name string, imageExts, videoExts map[string]bool) FileKind {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case imageExts[ext]:
		return KindImage
	case videoExts[ext]:
		return KindVideo
	default:
		return KindOther
	}
}

// countSubdirStats reads a subdirectory and returns the number
// of non-directory entries, the number of directories
// (including symlinks-to-directories), AND the total size
// (in bytes) of all non-directory entries DIRECTLY in the
// subdir (one level deep — NOT recursive into subdirs).
//
// Returns (0, 0, 0) on any error so the caller can still
// render the page (the columns just show "0").
//
// This is a single ReadDir call. For each entry:
//   - lstat → if real directory, count in dirs (size = 0
//     because the dir's own inode size is ~4 KB and not
//     meaningful; the actual contents are shown in nested
//     rows of the table)
//   - lstat → if symlink, follow with stat → if target is
//     directory, count in dirs
//   - otherwise (file, symlink-to-file, etc.), count in
//     items AND add the entry's size to totalSize
//
// Per user request 2026-06-27: # Dirs INCLUDES symlinks to
// directories (not just real directories). The reasoning is
// that from the visitor's perspective, both behave the same
// way (clicking enters the directory); the distinction is
// an implementation detail.
func countSubdirStats(path string) (items, dirs int, totalSize int64) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, 0, 0
	}
	for _, e := range entries {
		// Hidden files (starting with '.') are not counted
		// (consistent with how the gallery already filters
		// hidden files out of the visible file list).
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		// e.Info() returns lstat (symlink itself, not target).
		if info.Mode()&os.ModeSymlink != 0 {
			// Follow the symlink to determine what it points to.
			target, err := os.Stat(filepath.Join(path, e.Name()))
			if err != nil {
				// Broken symlink — skip
				continue
			}
			if target.IsDir() {
				dirs++
			} else {
				items++
				totalSize += target.Size()
			}
			continue
		}
		// Not a symlink — use lstat's IsDir directly.
		if info.IsDir() {
			dirs++
		} else {
			items++
			totalSize += info.Size()
		}
	}
	return items, dirs, totalSize
}

// Scan walks the directory and returns a SORTED slice of
// FileInfo. Both files and subdirectories are included
// (Kind = KindDir for directories). Symlinks are followed:
// a symlink to a directory is classified as KindDir, and the
// FileInfo's Size and ModTime come from the symlink's target
// (not the symlink itself, which would report the length of
// the target path string and the link's own mtime). Broken
// symlinks (target missing or inaccessible) are silently
// skipped.
//
// Sort order:
//   - "mtime" (default): newest first by modification time
//   - "name": alphabetical by name (case-insensitive)
//
// Returns an error only if the directory cannot be read.
//
// Per user report 2026-07-01: Scan is now FAST (~20ms for
// 4497 files). It does NOT read EXIF or pixel dimensions —
// those happen in the background via Enrich. The first
// page render after a fresh scan shows cards without the
// EXIF pill and dimensions watermark; subsequent renders
// (after the background enrich completes) show the full
// data. This trades a minor UX degradation on the FIRST
// visit for a major speedup on the critical path.
//
// Callers that need the enriched data should invoke
// s.EnrichInBackground(files) immediately after Scan
// returns. RenderPage works fine with the unenriched data
// — the Exif, Width, and Height fields just stay at their
// zero values.
func (s *Scanner) Scan() ([]FileInfo, error) {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil, err
	}
	out := make([]FileInfo, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			// Skip entries we can't stat (broken symlink, race, etc.)
			continue
		}
		// e.Info() is implemented via Lstat — it gives the FileInfo
		// of the symlink itself, not the target. For symlinks, follow
		// the link so we can classify the entry by its target's type
		// (a symlink to a directory should be shown as a directory)
		// and report the target's real size + mtime.
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Stat(filepath.Join(s.Root, e.Name()))
			if err != nil {
				// Broken symlink — skip
				continue
			}
			info = target
		}
		kind := KindOther
		if info.IsDir() {
			kind = KindDir
		} else {
			kind = Classify(e.Name(), s.ImageExts, s.VideoExts)
		}
		fi := FileInfo{
			Name:    e.Name(),
			ModTime: info.ModTime().UnixNano(),
			Size:    info.Size(),
			Kind:    kind,
		}
		// For subdirs, count the contents (items + subdirs).
		// Per user request 2026-06-27: this is what powers the
		// # Items and # Dirs columns in the dirs table. The cost
		// is one extra os.ReadDir per subdir, which is then
		// discarded; the counters are stored on the FileInfo.
		// We do this inline because: (a) it needs the filesystem,
		// not image parsing — fast; (b) the visitor always wants
		// this for the dirs table; (c) it's part of the per-dir
		// listing the user is already waiting for.
		if kind == KindDir {
			// Per user request 2026-06-27: the size column
			// in the dirs table shows the sum of file sizes
			// in the subdir (NOT the directory inode size, NOT
			// recursive). countSubdirStats does one ReadDir
			// and sums sizes as it goes.
			items, dirs, totalSize := countSubdirStats(filepath.Join(s.Root, e.Name()))
			fi.CountItems = items
			fi.CountDirs = dirs
			fi.Size = totalSize
		}
		out = append(out, fi)
	}
	if s.Sort == "name" {
		sort.Slice(out, func(i, j int) bool {
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		})
	} else {
		sort.Slice(out, func(i, j int) bool {
			return out[i].ModTime > out[j].ModTime
		})
	}
	return out, nil
}

// Enrich populates the EXIF and pixel-dimension fields on each
// FileInfo in files. This is the slow path — it reads image
// headers (~1-5ms per image for dimensions, ~1-5ms for EXIF)
// — and is meant to be called in a background goroutine via
// EnrichInBackground.
//
// For each image, Enrich tries the on-disk sidecar first
// (readExifCached / readDimensionsCached): if the sidecar
// exists and isn't stale, the read is ~50µs (a small text
// file). The slow path is the FIRST time we see an image —
// the sidecar doesn't exist yet, so we have to parse the
// image's header and write the sidecar. For 4491 images
// this can take ~45 seconds with one worker; parallelized
// to ~5 seconds with 10 workers.
//
// Errors are silently swallowed (logged to stderr). This is
// best-effort: missing EXIF data shows up as "no EXIF pill
// on the card", missing dimensions shows up as "no W × H
// watermark". The page still renders, just with less
// metadata.
//
// Safe to call concurrently with reads of the same files
// slice (e.g. when ScanCache.Get is serving requests while a
// previous Enrich is still running). The mutations are
// idempotent — a stale-or-younger value would just be
// overwritten. For race-free behavior, callers should pass
// a slice that NOBODY else is reading concurrently.
func (s *Scanner) Enrich(files []FileInfo) {
	if s.NoExif && s.ThumbCacheDir == "" {
		// Both enrichment paths are disabled — nothing to do.
		return
	}
	for i := range files {
		fi := &files[i]
		fullPath := filepath.Join(s.Root, fi.Name)
		// Subdirs: skip enrichment entirely. The CountItems /
		// CountDirs / Size fields are populated by Scan()'s
		// inline countSubdirStats call.
		if fi.Kind == KindDir {
			continue
		}
		// EXIF for images (no-op when NoExif is set).
		if fi.Kind == KindImage && !s.NoExif {
			exif, err := readExifCached(fullPath, s.ThumbCacheDir, s.ThumbFormat)
			if err == nil {
				fi.Exif = exif
			}
		}
		// Pixel dimensions for images and videos.
		if fi.Kind == KindImage || fi.Kind == KindVideo {
			thumbExt := s.ThumbFormat
			if thumbExt == "" {
				thumbExt = "webp"
			}
			w, h, err := readDimensionsCached(fullPath, s.ThumbCacheDir, thumbExt)
			if err == nil && w > 0 && h > 0 {
				fi.Width = w
				fi.Height = h
			}
		}
	}
}

// EnrichInBackground spawns a goroutine that calls Enrich
// on files in parallel. Returns immediately. Use this after
// Scan() when the cached files list is returned to the
// caller and you want to fill in the EXIF/dimensions async.
//
// maxParallel workers process files concurrently. The default
// of 8 was chosen empirically: image-header parsing is
// CPU-bound (Go's stdlib decoders), 8 workers saturates a
// typical 4-8 core machine without causing too much disk
// thrashing on slow storage. Thumbs are still served from
// a separate goroutine pool (EagerGenPageThumbs) and aren't
// blocked by this — they only need the source file, not
// the EXIF/dimensions cache.
//
// For 4500 files at ~10ms each: 4500/8 * 10ms = ~5.6 seconds
// of background work per fresh scan (vs ~45s single-threaded).
// The visitor's first page render isn't blocked by this —
// the HTML response is sent as soon as Scan() returns.
func (s *Scanner) EnrichInBackground(files []FileInfo) {
	go s.enrichParallel(files, 8)
}

// enrichParallel runs Enrich across the file list using a
// worker pool of `workers` goroutines. Each worker pulls
// indices off a channel until exhausted. Errors are ignored.
func (s *Scanner) enrichParallel(files []FileInfo, workers int) {
	if workers < 1 {
		workers = 1
	}
	thumbExt := s.ThumbFormat
	if thumbExt == "" {
		thumbExt = "webp"
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i := range files {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()
			fi := &files[idx]
			if fi.Kind == KindDir {
				return
			}
			fullPath := filepath.Join(s.Root, fi.Name)
			if fi.Kind == KindImage && !s.NoExif {
				exif, err := readExifCached(fullPath, s.ThumbCacheDir, s.ThumbFormat)
				if err == nil {
					fi.Exif = exif
				}
			}
			if fi.Kind == KindImage || fi.Kind == KindVideo {
				w, h, err := readDimensionsCached(fullPath, s.ThumbCacheDir, thumbExt)
				if err == nil && w > 0 && h > 0 {
					fi.Width = w
					fi.Height = h
				}
			}
		}(i)
	}
	wg.Wait()
}

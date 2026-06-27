package gallery

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileKind categorises an entry in a gallery directory.
type FileKind string

const (
	// KindDir is a subdirectory. The Name is the directory basename
	// (not the full path); the scanner joins it to Root at render
	// time.
	KindDir FileKind = "dir"
	// KindImage is an image file (jpg, png, gif, webp, svg, avif).
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
var defaultImageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true,
	".gif": true, ".webp": true, ".svg": true, ".avif": true,
	".heic": true,
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
	// CountItems is the number of NON-directory entries
	// (files, symlinks-to-files, broken symlinks, etc.) inside
	// this subdirectory. Only meaningful for KindDir entries.
	// Populated by Scanner.Scan; the count is computed by
	// reading the subdir's contents (one extra os.ReadDir
	// syscall per subdir, then discarded).
	CountItems int `json:"count_items"`
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
// provided, the defaults are used (jpg/png/gif/webp/svg/avif/heic
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

// Scan walks the directory and returns a sorted slice of FileInfo.
// Both files and subdirectories are included (Kind = KindDir for
// directories). Symlinks are followed: a symlink to a directory
// is classified as KindDir, and the FileInfo's Size and ModTime
// come from the symlink's target (not the symlink itself, which
// would report the length of the target path string and the
// link's own mtime). Broken symlinks (target missing or
// inaccessible) are silently skipped.
//
// Sort order:
//   - "mtime" (default): newest first by modification time
//   - "name": alphabetical by name (case-insensitive)
//
// Returns an error only if the directory cannot be read.
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

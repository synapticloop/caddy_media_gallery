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

// imageExts are file extensions that the gallery treats as images.
var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true,
	".gif": true, ".webp": true, ".svg": true, ".avif": true,
}

// videoExts are file extensions that the gallery treats as videos.
var videoExts = map[string]bool{
	".mp4": true, ".webm": true,
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
}

// Scanner reads a directory and produces a sorted []FileInfo.
// Both directories and files are included; the Kind field tells
// them apart.
type Scanner struct {
	Root string
	Sort string // "mtime" (default) or "name"
}

// NewScanner returns a Scanner for the given root directory with
// default sort order (mtime desc — newest first).
func NewScanner(root string) *Scanner {
	return &Scanner{Root: root, Sort: "mtime"}
}

// Classify returns the FileKind for a filename based on its
// extension. Directories are not classified by name; the scanner
// uses the entry's IsDir() to set KindDir directly.
func Classify(name string) FileKind {
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

// Scan walks the directory and returns a sorted slice of FileInfo.
// Both files and subdirectories are included (Kind = KindDir for
// directories).
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
		kind := KindOther
		if e.IsDir() {
			kind = KindDir
		} else {
			kind = Classify(e.Name())
		}
		out = append(out, FileInfo{
			Name:    e.Name(),
			ModTime: info.ModTime().UnixNano(),
			Size:    info.Size(),
			Kind:    kind,
		})
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

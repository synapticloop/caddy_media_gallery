package gallery

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileKind categorises a file for gallery rendering.
type FileKind string

const (
	KindImage FileKind = "image"
	KindVideo FileKind = "video"
	KindOther FileKind = "other"
)

// Image extensions. Lower-case, with the leading dot.
var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true,
	".gif": true, ".webp": true, ".svg": true, ".avif": true,
}

// Video extensions. Lower-case, with the leading dot.
var videoExts = map[string]bool{
	".mp4": true, ".webm": true,
}

// FileInfo describes a single file in a gallery directory. This is the
// type that flows into the template renderer.
type FileInfo struct {
	Name    string   `json:"name"`
	ModTime int64    `json:"mtime"` // unix nanoseconds — int64 keeps the type JSON-friendly (no time.Time marshalling quirks) and nanosecond resolution preserves sub-second ordering of files written close together
	Size    int64    `json:"size"`
	Kind    FileKind `json:"kind"`
}

// Scanner reads a directory and produces a sorted []FileInfo.
type Scanner struct {
	Root string
	Sort string // "mtime" (default) or "name"
}

// NewScanner returns a Scanner for the given root directory with default
// sort order (mtime desc — newest first).
func NewScanner(root string) *Scanner {
	return &Scanner{Root: root, Sort: "mtime"}
}

// Classify returns the FileKind for a filename based on its extension.
// Exported because tests + templates may want to use it.
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
// Directories are skipped. Sort order:
//   - "mtime" (default): newest first by modification time
//   - "name": alphabetical by name (case-insensitive, A-Z)
//
// Returns an error only if the directory cannot be read.
func (s *Scanner) Scan() ([]FileInfo, error) {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil, err
	}
	out := make([]FileInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			// Skip files we can't stat (broken symlink, race condition, etc.)
			continue
		}
		out = append(out, FileInfo{
			Name:    e.Name(),
			ModTime: info.ModTime().UnixNano(),
			Size:    info.Size(),
			Kind:    Classify(e.Name()),
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

package gallery

import (
	"bytes"
	"fmt"
	"html/template"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// PageData is the top-level template data for a gallery page.
// All values are pre-formatted for direct template use — no
// template-level computations needed.
type PageData struct {
	Title       string
	PathPrefix  string // e.g. "./" — prefix for relative links
	ThumbPrefix string // e.g. "./_thumbs/" — prefix for thumb URLs

	// Three sections. OtherFiles is shown in full regardless of
	// pagination/sort (per the user's spec — it always appears
	// at the top, horizontal). Images is the paginated/sorted
	// set. The directories section is split into two
	// elements: Up (the synthetic ../ entry, rendered on its
	// own line, always first) and Subdirs (the actual subdirs,
	// rendered in a tight row with no gap between them, per
	// the user's 2026-06-17 spec).
	Up         *FileView  // the up entry, or nil at the gallery root
	Subdirs    []FileView // the actual subdirs (no up entry)
	OtherFiles []FileView
	Images     []FileView

	// Pagination
	Page     int // 1-based
	PageSize int
	// Query is the current URL query (sort, filter, page, breadcrumb, etc.) — passed to the template so the page-size form can include hidden inputs to preserve other params on submit.
	Query url.Values
	// SearchQuery is the raw ?q= value (for the search
	// input's `value=""` attribute and for hidden inputs
	// that preserve the query on form submit).
	SearchQuery string
	// PageSizes is the list of per-page options the visitor
	// can choose from in the dropdown (e.g. [30, 60, 120, "all"]).
	// Configured via the `page_sizes` Caddyfile directive;
	// defaults to [30, 60, 120, "all"].
	PageSizes []string
	// TotalImages is the total media count (images + videos)
	// — used for the pagination math and the visibility check
	// on the images grid section.
	TotalImages int
	// ImageCount is the count of image files only — used for
	// the "N images" label in the header meta line (so the
	// label is accurate; videos are no longer miscounted as
	// images). Per user request 2026-06-17: separate video
	// indicator in the header.
	ImageCount int
	// ImageStart and ImageEnd are the 1-based range of images
	// shown on the current page (e.g. 1-60 for page 1, 61-89
	// for page 2). Used in the media section header: "Media
	// (89 - Showing 1-60)". Both are 0 if there are no images.
	ImageStart int
	ImageEnd   int
	// TotalVideos is the count of video files only — shown in
	// the header meta line as "N videos" (after the images
	// count, only if > 0).
	TotalVideos int
	// TotalFiles is the sum of all files in the directory:
	// ImageCount + TotalVideos + len(OtherFiles). Computed
	// in RenderPage (not the template) and shown at the start
	// of the header meta line as "N files" (per user request
	// 2026-06-19: a quick "how many files are in this dir"
	// answer at the top of the meta line).
	TotalFiles int
	// TotalAllFilesSize is the pre-formatted (via humanSize) total
	// size of ALL files in the directory: images + videos + other
	// files. Excludes subdirectories (which don't have a Size
	// field). Shown in the header meta line as a separate segment
	// wrapped in `//` separators, per user request 2026-06-18:
	//   "the X.X KB is the total for all files in the directory"
	// e.g. "34 images ·8 videos ·2 other files // (8.3 MB) //
	//        ·26 directories ·50 per page"
	// The `//` separators visually distinguish the size from the
	// other meta items (which use `·`). Operator sees at a glance
	// how much disk the whole directory's media + sidecar files
	// take.
	TotalAllFilesSize string
	TotalPages        int
	HasPrev           bool
	HasNext           bool
	// PageNumbers is the list of page numbers (and 0 for
	// ellipsis) to show in the Google-style bottom pagination.
	// Computed by pageNumbers(current, total). Empty when
	// total <= 1 (no pagination needed).
	PageNumbers []int

	// Breadcrumb is the list of path segments from the
	// gallery root to the current directory. Each segment has
	// a Name (human-readable) and an Href (relative URL).
	// The first segment is the gallery root; the last is the
	// current directory (rendered as plain text, not a link).
	Breadcrumb []BreadcrumbSegment

	// Sort
	Sort SortSpec

	// TypeFilter is the parsed ?type= query param (set of
	// file extensions to show). nil = no filter (show all
	// files). An empty (non-nil) map = "show nothing"
	// (the filter UI shows the empty state). Use
	// IsTypeFilterActive to distinguish "no filter" from
	// "filter that selects nothing" in the UI.
	TypeFilter map[string]bool

	// IsTypeFilterActive is true if the URL has a non-empty
	// ?type= query param. This is the source of truth for
	// whether the filter UI should show the "filtered" state.
	IsTypeFilterActive bool

	// TypeFilterQuery is the raw ?type= value (or "" if no
	// filter). Pass through to the filter UI's form action
	// so the user can re-submit their selection.
	TypeFilterQuery string

	// FilterImageOptions / FilterVideoOptions / FilterOtherOptions
	// are the three filter dropdowns (Images / Videos / Other).
	// Each contains the extensions present in the current
	// directory, with their counts, marked as Selected if
	// currently in the active filter. The UI renders these
	// as three side-by-side dropdowns + an Apply button.
	FilterImageOptions FilterGroup
	FilterVideoOptions FilterGroup
	FilterOtherOptions FilterGroup
}

// FileView is the template-friendly representation of a single
// entry. All display strings are pre-formatted (no template
// computation needed). Href and ThumbURL are relative to the
// current page.
type FileView struct {
	Name     string // basename ("photo.jpg" or "subdir")
	Href     string // relative link
	ThumbURL string // for images, the relative thumb URL; empty for non-images
	IsDir    bool
	IsUp     bool // true for the synthetic "../" up-link entry (rendered with ↑ icon, no trailing /)
	IsImage  bool
	IsVideo  bool
	IsOther  bool

	// ParentDir is set ONLY on the up-link FileView. It is the
	// basename of the parent directory (one level up from the
	// current page). Rendered as part of the up chip's display
	// text — "Up (../{ParentDir})" — so the user sees which
	// directory they'll land in. Empty at the gallery root or
	// in a top-level subdir (parent is the gallery root, no name
	// to show). Per user request 2026-06-17.
	ParentDir string

	// Internal fields used by buildFileView to format Size/Date
	// without round-tripping through fmt in the template.
	Size string
	Date string
	Type string

	// ModTime is the raw int64 timestamp (nanoseconds since
	// epoch). Used by the JS header-sort feature to sort
	// the others table by Modified. Formatted display goes
	// through Date (the human-readable form).
	ModTime int64

	// Width and Height are the pixel dimensions of the source
	// image/video. Zero for unsupported formats (AVIF, HEIC,
	// SVG — per user request 2026-06-27). Dimensions is the
	// formatted "WIDTH × HEIGHT" string for the watermark;
	// empty when Width or Height is zero.
	Width      int
	Height     int
	Dimensions string

	// Exif holds the CAMERA-subset EXIF metadata for this
	// file. nil means "no EXIF" — the template should render
	// neither the "EXIF" pill on the card nor the EXIF panel
	// in the lightbox. Populated by buildFileView from
	// FileInfo.Exif (which is populated by scanner.Scan via
	// readExif). Per user request 2026-06-27: GPS fields
	// are NEVER extracted.
	Exif *ExifData

	// CountItems is the number of NON-directory entries inside
	// this subdirectory (files + symlinks to files + broken
	// symlinks, etc.). Only set on directory entries (IsDir=true).
	// Populated by buildFileView from FileInfo.CountItems
	// (set by Scanner.Scan via countSubdirStats).
	CountItems int
	// CountDirs is the number of directories inside this
	// subdirectory. Includes real directories AND symlinks to
	// directories (per user request 2026-06-27). Only set on
	// directory entries.
	CountDirs int
}

// BreadcrumbSegment is one segment of the breadcrumb path.
// Each segment represents a directory level; Name is the
// human-readable label, Href is the URL to navigate to that
// level. The last segment (the current directory) is rendered
// as plain text (not a link) by the template.
type BreadcrumbSegment struct {
	Name string
	Href string
}

// computeBreadcrumb returns the breadcrumb segments for the
// current gallery view. The first segment is the gallery root
// (no path beyond /, links to "./"). Each subsequent segment
// is one path component deeper; its Href is the relative URL
// to that level (e.g. "./photos/" for the "photos" subdir).
//
// The relPath is the URL path after the leading slash, with
// the trailing slash preserved (it's already in this format
// from gallery.go's normalisation). We split on "/" and
// accumulate the path. A trailing empty string (from the
// trailing slash) is dropped.
//
// Examples:
//
//	relPath ""                    -> [{Name: "images", Href: "./"}]
//	relPath "photos/"             -> [{Name: "images", Href: "./"},
//	                                  {Name: "photos", Href: "./photos/"}]
//	relPath "photos/2024/maui/"   -> [{Name: "images", Href: "./"},
//	                                  {Name: "photos", Href: "./photos/"},
//	                                  {Name: "2024",   Href: "./photos/2024/"},
//	                                  {Name: "maui",   Href: "./photos/2024/maui/"}]
//
// The first segment's Name comes from the title arg (the
// gallery's root title, e.g. "images" for /images/ or the
// basename of the root dir for top-level galleries).
// computeBreadcrumb returns the breadcrumb segments for the
// current gallery view. The first segment is the GALLERY ROOT
// (the first path component of relPath — e.g. "images" for a
// gallery mounted at /images/*). Each subsequent segment is one
// path component deeper; the last is the current directory
// (rendered as plain text, not a link).
//
// The relPath is the URL path after the leading slash, with
// the trailing slash preserved. We split on "/" and:
//   - If relPath is empty, return just the title (the gallery
//     is at the root with no subdirs).
//   - Otherwise, use the FIRST segment of relPath as the root
//     name (this is the gallery's mount point in the URL —
//     e.g. "images" for /images/animals/). The remaining
//     segments become the breadcrumb path.
//
// Per user request 2026-06-20: the previous version used
// `title` (the current dir's basename) as the root name, which
// produced wrong breadcrumb sequences like "animals / images
// / media_gallery / animals" (first and last were the same).
// Using the first relPath segment as the root fixes this.
//
// Examples:
//
//	relPath ""                  -> [{Name: <title>, Href: "./"}]
//	relPath "images/"           -> [{Name: "images", Href: "./"}]
//	relPath "images/photos/"   -> [{Name: "images", Href: "./"},
//	                                {Name: "photos", Href: "./photos/"}]
//	relPath "images/photos/2024/maui/"
//	                            -> [{Name: "images", Href: "./"},
//	                                {Name: "photos", Href: "./photos/"},
//	                                {Name: "2024",   Href: "./photos/2024/"},
//	                                {Name: "maui",   Href: "./photos/2024/maui/"}]
//
// computeBreadcrumb returns the breadcrumb segments for the
// current gallery view. The first segment is the gallery's
// URL mount (passed in as breadcrumbRoot, e.g. "images" for a
// gallery mounted at /images/*). Each subsequent segment is
// one path component deeper; the last is the current directory
// (rendered as plain text, not a link).
//
// The relPath is the URL path AFTER the leading slash, with
// the trailing slash preserved. Caddy's `handle_path /images/*`
// strips the mount prefix BEFORE the handler runs, so relPath
// no longer contains the mount segment. The breadcrumbRoot
// argument is the only way the breadcrumb knows what the mount
// prefix is — that's why RenderPage passes it explicitly.
//
// Per user request 2026-06-20: the previous version used
// the FIRST segment of relPath as the root, but that was
// wrong because the mount prefix is stripped before
// relPath is computed. The new version uses the breadcrumbRoot
// argument (which is the basename of the gallery's filesystem
// root, e.g. "images" for /var/www/html/images) so the
// breadcrumb's first segment matches what the user sees in
// the URL.
//
// Examples:
//
//	breadcrumbRoot="images", relPath ""
//	                          -> [{Name: "images", Href: "./"}]
//	breadcrumbRoot="images", relPath "photos/"
//	                          -> [{Name: "images", Href: "./"},
//	                              {Name: "photos", Href: "./photos/"}]
//	breadcrumbRoot="images", relPath "media_gallery/animals/"
//	                          -> [{Name: "images", Href: "./"},
//	                              {Name: "media_gallery", Href: "./media_gallery/"},
//	                              {Name: "animals", Href: "./media_gallery/animals/"}]
func computeBreadcrumb(relPath, title, pathPrefix, breadcrumbRoot, absolutePrefix string) []BreadcrumbSegment {
	// Root: the breadcrumbRoot argument (the gallery's URL
	// mount prefix, e.g. "images"). If it's empty for some
	// reason, fall back to the title.
	rootName := breadcrumbRoot
	if rootName == "" {
		rootName = title
	}
	// Determine the root Href. If absolutePrefix is set (operator
	// configured path_prefix in the Caddyfile), use it as the
	// absolute URL prefix for all breadcrumb links. Otherwise,
	// fall back to pathPrefix (relative "./") for backwards-
	// compatible behaviour.
	rootHref := pathPrefix
	if absolutePrefix != "" {
		rootHref = absolutePrefix
	}
	out := []BreadcrumbSegment{}
	out = append(out, BreadcrumbSegment{Name: rootName, Href: rootHref})
	if relPath == "" {
		return out
	}
	// Strip trailing slash for splitting.
	trimmed := strings.TrimSuffix(relPath, "/")
	if trimmed == "" {
		return out
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) == 0 {
		return out
	}
	// Build cumulative Href for each segment.
	// - Absolute (absolutePrefix set): links are "/images/seg1/",
	//   "/images/seg1/seg2/", etc. — work from any page.
	// - Relative (absolutePrefix empty): links are "./seg1/",
	//   "./seg1/seg2/", etc. — only work from the current dir.
	acc := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if acc == "" {
			acc = p
		} else {
			acc = acc + "/" + p
		}
		var href string
		if absolutePrefix != "" {
			href = absolutePrefix + acc + "/"
		} else {
			href = pathPrefix + acc + "/"
		}
		out = append(out, BreadcrumbSegment{
			Name: p,
			Href: href,
		})
	}
	return out
}

// SortSpec describes the current sort state. Field is one of
// "name", "type", "date", "size", or "mtime" (the default).
// Order is "asc" or "desc".
type SortSpec struct {
	Field string
	Order string
}

// humanSize returns a human-readable size string using 1024-based
// units (KB, MB, GB, TB). Examples: 800 → "800 B",
// 1500 → "1.5 KB", 1572864 → "1.5 MB".
func humanSize(n int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
		TB = 1024 * GB
	)
	switch {
	case n < KB:
		return fmt.Sprintf("%d B", n)
	case n < MB:
		return fmt.Sprintf("%.1f KB", float64(n)/KB)
	case n < GB:
		return fmt.Sprintf("%.1f MB", float64(n)/MB)
	case n < TB:
		return fmt.Sprintf("%.2f GB", float64(n)/GB)
	default:
		return fmt.Sprintf("%.2f TB", float64(n)/TB)
	}
}

// formatDate converts a unix-nanosecond ModTime to an ISO date
// string (YYYY-MM-DD). Always in UTC so the date is stable across
// server timezones (the system is AEST/UTC+10; without UTC
// normalisation a file modified at 23:30 UTC would render as the
// next day locally). Returns "" for zero values.
func formatDate(unixNano int64) string {
	if unixNano == 0 {
		return ""
	}
	return time.Unix(0, unixNano).UTC().Format("2006-01-02")
}

// formatType returns the file's extension, uppercase, without
// the leading dot ("JPG" for "photo.jpg"). For directories it
// returns "DIR".
func formatType(name string, isDir bool) string {
	if isDir {
		return "DIR"
	}
	ext := strings.TrimPrefix(filepath.Ext(name), ".")
	return strings.ToUpper(ext)
}

// buildFileView creates a FileView from a FileInfo. pathPrefix is
// the relative URL prefix for links ("./" for the gallery root);
// thumbPrefix is the relative URL prefix for thumb URLs
// ("./_thumbs/" for the gallery root).
// buildFileView converts a FileInfo into a template-friendly
// FileView. The thumb URL is normally `thumbPrefix/<basename>.webp`;
// when noThumbs is true, images use the original file URL as the
// (for template compatibility) but its value is the original file
// path in this case.
//
// noThumbs applies to IMAGE thumbnails. noVideoThumbs applies to
// VIDEO thumbnails (independently togglable). A video thumb URL
// is set only if noVideoThumbs is false; when true, the video card
// shows the placeholder gradient + play button (no <img>).
func buildFileView(f FileInfo, pathPrefix, thumbPrefix string, noThumbs, noVideoThumbs bool) FileView {
	v := FileView{
		Name: f.Name,
		Type: formatType(f.Name, f.Kind == KindDir),
	}
	switch f.Kind {
	case KindDir:
		v.IsDir = true
		v.Href = pathPrefix + f.Name + "/"
		// Per user request 2026-06-19: directories have a
		// Modified date in the dirs table.
		v.Date = formatDate(f.ModTime)
		// Per user request 2026-06-27: the dirs table's Size
		// column shows the sum of file sizes DIRECTLY in the
		// subdir (NOT recursive into nested subdirs, NOT the
		// directory inode size). Scanner.Scan computes this
		// via countSubdirStats and stores the result in
		// FileInfo.Size (overwriting the directory's own
		// inode size, which is typically 4KB and not useful
		// for the visitor). The humanSize() formatter
		// displays it in human-readable form.
		v.Size = humanSize(f.Size)
		// Per user request 2026-06-27: # Items and # Sub-Dirs
		// show the contents of the subdir. Populated by
		// Scanner.Scan via countSubdirStats (one extra
		// ReadDir per subdir).
		v.CountItems = f.CountItems
		v.CountDirs = f.CountDirs
	case KindImage:
		v.IsImage = true
		v.Href = pathPrefix + f.Name
		if noThumbs {
			// Use the original image as the "thumb" (no thumb
			// generation). The template still uses {{.ThumbURL}}
			// as the <img src>, so the field name stays the same;
			// its value just points at the original file.
			v.ThumbURL = pathPrefix + f.Name
		} else {
			v.ThumbURL = thumbPrefix + thumbStripExt(f.Name) + ".webp"
		}
		v.Size = humanSize(f.Size)
		v.Date = formatDate(f.ModTime)
	case KindVideo:
		v.IsVideo = true
		v.Href = pathPrefix + f.Name
		// Video thumb: set ThumbURL only if video thumb generation
		// is enabled. (The serveThumb handler will dispatch to
		// ffmpeg when it sees this URL; if ffmpeg is missing at
		// the host, the handler returns 404. This is the same
		// behavior as image thumbs — the URL is set, but the
		// generator may fail at request time.)
		if !noVideoThumbs {
			v.ThumbURL = thumbPrefix + thumbStripExt(f.Name) + ".webp"
		}
		v.Size = humanSize(f.Size)
		v.Date = formatDate(f.ModTime)
	default:
		v.IsOther = true
		v.Href = pathPrefix + f.Name
		v.Size = humanSize(f.Size)
		v.Date = formatDate(f.ModTime)
		// Per user request 2026-06-27: expose the raw ModTime
		// to the JS header-sort feature. The dirs and others
		// tables have data-date="..." attributes for sorting.
		v.ModTime = f.ModTime
	}
	// Copy EXIF data through (regardless of kind — the template
	// uses {{if .Exif.HasAny}} to decide whether to render the
	// EXIF pill and the lightbox panel). Per user request
	// 2026-06-27: GPS fields are NEVER extracted; see exif.go.
	v.Exif = f.Exif
	// Copy dimensions through. Per user request 2026-06-27:
	// the bottom-right watermark shows the source file's
	// W × H. The formatDimensions helper returns an empty
	// string when either dimension is zero, which the
	// template treats as "don't render the watermark".
	v.Width = f.Width
	v.Height = f.Height
	v.Dimensions = formatDimensions(f.Width, f.Height)
	return v
}

// thumbStripExt strips the file extension ("photo.jpg" → "photo").
// Extracted so buildFileView can use it without pulling the full
// template funcMap in.
func thumbStripExt(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// splitFiles partitions a []FileInfo into dirs / others / images.
// Per the user's spec, VIDEOS go in the image grid (with a
// play-button thumbnail), not in the "Other files" strip.
// "Others" is therefore only non-media files (HTML, txt, etc.).
//
// Dirs are always sorted case-insensitive ascending by name here,
// independent of the scanner's sort or the user's image-sort
// choice — the directory strip is a stable navigation aid and
// shouldn't reshuffle when the user changes how the images are
// sorted. The user explicitly asked for this in 2026-06-14:
// "the directory list should be in alphabetical order, and if
// any ordering is applied to the images, this will not affect
// the directory listing."
//
// Others are returned in scanner order (which respects the user's
// sort choice by default — same as images). The ".." up entry
// for subdirs is prepended in RenderPage after this returns.
func splitFiles(files []FileInfo) (dirs, others, images []FileInfo) {
	for _, f := range files {
		switch f.Kind {
		case KindDir:
			dirs = append(dirs, f)
		case KindImage, KindVideo:
			images = append(images, f)
		default:
			others = append(others, f)
		}
	}
	// Directories are always alphabetical (case-insensitive),
	// regardless of how the user sorted the image grid.
	sort.SliceStable(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})
	// Other files are NOT sorted here — they get sorted by
	// sortFiles() in RenderPage using the user's current sort
	// selection. Per user request 2026-06-20: "the other files
	// should respond to the sorting, the directories never
	// respond to the sorting, they are always in alphabetical
	// order". So dirs = always alpha (above); others = obey
	// the user's sort selection (in RenderPage).
	return
}

// sortFiles sorts a slice of FileInfo by the given spec. The
// slice is sorted in place. For all fields including "mtime",
// we sort from scratch here. The scanner DOES pre-sort by
// mtime desc (its default), so this means a second sort pass
// for the mtime-desc case — but that pass is O(n log n) on
// 50-200 items (microseconds) and keeps sortFiles correct
// regardless of the caller's input order. Earlier this
// function returned early for "mtime", which (a) silently
// ignored `order=asc` and (b) was fragile to callers passing
// pre-sorted data.
func sortFiles(files []FileInfo, spec SortSpec) {
	asc := spec.Order == "asc"
	switch spec.Field {
	case "mtime", "date", "":
		sort.SliceStable(files, func(i, j int) bool {
			if asc {
				return files[i].ModTime < files[j].ModTime
			}
			return files[i].ModTime > files[j].ModTime
		})
	case "name":
		sort.SliceStable(files, func(i, j int) bool {
			ci, cj := strings.ToLower(files[i].Name), strings.ToLower(files[j].Name)
			if asc {
				return ci < cj
			}
			return ci > cj
		})
	case "type":
		sort.SliceStable(files, func(i, j int) bool {
			ti, tj := formatType(files[i].Name, files[i].Kind == KindDir),
				formatType(files[j].Name, files[j].Kind == KindDir)
			if asc {
				return ti < tj
			}
			return ti > tj
		})
	case "size":
		sort.SliceStable(files, func(i, j int) bool {
			if asc {
				return files[i].Size < files[j].Size
			}
			return files[i].Size > files[j].Size
		})
	}
}

// paginate returns the slice of files for the given page (1-based).
// Returns an empty slice if page is out of range.
// Per user request 2026-06-20: the default page size changed
// from 50 to 60 (nicely divisible by 2, 3, 4, 5, 6).
func paginate(files []FileInfo, page, pageSize int) []FileInfo {
	if pageSize <= 0 {
		pageSize = 60
	}
	if page < 1 {
		page = 1
	}
	start := (page - 1) * pageSize
	if start >= len(files) {
		return nil
	}
	end := start + pageSize
	if end > len(files) {
		end = len(files)
	}
	return files[start:end]
}

// validatePageSize ensures the requested pageSize is in the
// pageSizes list (the operator-configured dropdown options).
// If pageSizes is empty (default), accepts any positive int.
// If the requested value is "all", converts to 0 (which means
// "no pagination" downstream).
// Returns the validated pageSize, or the first item in the
// list (parsed as int, or 0 if it is "all") if the requested
// value is not in the list.
func validatePageSize(requested int, pageSizes []string) int {
	if len(pageSizes) == 0 {
		// No operator override; accept any positive int.
		if requested <= 0 {
			return 60
		}
		return requested
	}
	// Parse the configured sizes (skip "all" for the "find"
	// step - it represents no pagination limit).
	var firstValid int
	firstSet := false
	for _, s := range pageSizes {
		if s == "all" {
			if !firstSet {
				firstValid = 0
				firstSet = true
			}
			continue
		}
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			continue
		}
		if !firstSet {
			firstValid = n
			firstSet = true
		}
		if requested == n {
			return n
		}
	}
	// Special-case: requested = 0 means "all" (no pagination).
	hasAll := false
	for _, s := range pageSizes {
		if s == "all" {
			hasAll = true
			break
		}
	}
	if requested == 0 && hasAll {
		return 0
	}
	// Not found: fall back to the first valid value.
	if firstSet {
		return firstValid
	}
	return 60
}

// pageNumbers returns the list of page numbers (and 0 for
// ellipsis) to show in the Google-style pagination nav. Standard
// pattern:
//   - ≤ 10 pages total: show all
//   - current near start (current ≤ 4): 1 2 3 4 5 ... N
//   - current near end (current ≥ N-3): 1 ... N-4 N-3 N-2 N-1 N
//   - otherwise: 1 ... current-1 current current+1 ... N
//
// The 0 entries are rendered as "..." in the template. Per user
// request 2026-06-17: replace the bottom pagination "← Prev |
// Page 1 of 2 | Next →" with a Google-style numbered list.
func pageNumbers(current, total int) []int {
	// For small totals, just show every page — no ellipsis needed.
	if total <= 10 {
		pages := make([]int, total)
		for i := 0; i < total; i++ {
			pages[i] = i + 1
		}
		return pages
	}

	pages := []int{1}

	switch {
	case current <= 4:
		// Near start: 1 2 3 4 5 ... N
		for i := 2; i <= 5; i++ {
			pages = append(pages, i)
		}
		pages = append(pages, 0, total)
	case current >= total-3:
		// Near end: 1 ... N-4 N-3 N-2 N-1 N
		pages = append(pages, 0)
		for i := total - 4; i <= total; i++ {
			pages = append(pages, i)
		}
	default:
		// In the middle: 1 ... current-1 current current+1 ... N
		pages = append(pages, 0, current-1, current, current+1, 0, total)
	}

	return pages
}

// parseSort reads sort and order from the query string, with a
// safe default. Unknown fields fall back to the mtime default.
func parseSort(q url.Values) SortSpec {
	field := q.Get("sort")
	switch field {
	case "name", "type", "date", "size", "mtime":
	default:
		field = "mtime"
	}
	order := q.Get("order")
	if order != "asc" && order != "desc" {
		order = "desc"
	}
	return SortSpec{Field: field, Order: order}
}

// pageFromQuery returns the 1-based page number from the query,
// clamped to [1, ...]. Returns 1 on parse failure.
func pageFromQuery(q url.Values) int {
	page, err := parseIntDefault(q.Get("page"), 1)
	if err != nil || page < 1 {
		return 1
	}
	return page
}

// applySearchFilter returns a copy of files with only the
// entries whose filename matches the query. Directories
// (KindDir) are NEVER filtered out by search — the user
// should still be able to navigate up/down even when the
// search doesn't match the directory name.
func applySearchFilter(files []FileInfo, query []string) []FileInfo {
	if len(query) == 0 {
		return files
	}
	out := make([]FileInfo, 0, len(files))
	for _, f := range files {
		if f.Kind == KindDir {
			out = append(out, f)
			continue
		}
		if filenameMatchesQuery(f.Name, query) {
			out = append(out, f)
		}
	}
	return out
}

// sortPageSizes returns a sorted copy of the page size
// list. Numeric values are sorted ascending by their integer
// value; the special token "all" always sorts to the END.
// Any other non-numeric value sorts to the very end (after
// "all"). This way the operator can write the list in any
// order and the dropdown display is consistent.
func sortPageSizes(sizes []string) []string {
	if len(sizes) == 0 {
		return sizes
	}
	out := make([]string, len(sizes))
	copy(out, sizes)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		// "all" always sorts last.
		if a == "all" {
			return false
		}
		if b == "all" {
			return true
		}
		// Numeric values sort ascending.
		aNum, aErr := strconv.Atoi(a)
		bNum, bErr := strconv.Atoi(b)
		if aErr == nil && bErr == nil {
			return aNum < bNum
		}
		// Non-numeric: keep insertion order (slice stable
		// already does this). If one is numeric, the numeric
		// one comes first.
		if aErr != nil && bErr != nil {
			return a < b
		}
		if aErr != nil {
			return false
		}
		return true
	})
	return out
}

// parseSearchQuery splits a raw search query string into
// normalized word tokens. Whitespace-separated; lowercased;
// empty/whitespace-only returns an empty slice (meaning
// "no filter").
func parseSearchQuery(q string) []string {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil
	}
	return strings.Fields(q)
}

// filenameMatchesQuery returns true when the filename
// matches the (already-parsed) query words under the
// word-boundary rule:
//
//  1. Lowercase both sides.
//  2. Split the filename on `_`, `-`, and ` ` (each of
//     these is treated as a word separator). Split the
//     query on whitespace (already done by parseSearchQuery).
//  3. A match occurs when any filename "word" starts with
//     any query word.
//
// Empty query = always true (no filter). Examples (query
// "cat" → matches `cat.jpg`, `cat-photo.jpg`, `my_cat.webp`,
// `category-icon.svg` but NOT `scatter.png`).
func filenameMatchesQuery(filename string, query []string) bool {
	if len(query) == 0 {
		return true
	}
	name := strings.ToLower(filename)
	// Split the filename on common word separators so each
	// segment is treated as a discrete "word".
	words := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '-' || r == ' '
	})
	for _, w := range words {
		for _, q := range query {
			if strings.HasPrefix(w, q) {
				return true
			}
		}
	}
	return false
}

// pageSizeFromQuery returns the per-page size from the URL
// query (?page_size=N). Returns -1 if not specified or invalid
// (the caller will then use the configured default). The
// special token "all" is converted to 0 (which means "no
// pagination" downstream).
func pageSizeFromQuery(q url.Values) int {
	raw := strings.TrimSpace(q.Get("page_size"))
	if raw == "" {
		return -1
	}
	if raw == "all" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return -1
	}
	return n
}

// queryString renders url.Values as a URL query string
// (without the leading "?"). Empty string if the query has
// no keys. Uses literal "=" and "&" (not URL-encoded) so
// the HTML source is readable. Values are still html-escaped
// for safety against injection, but the structural chars
// are unencoded.
//
// Used by link builders (pagination, sort bar, filter
// buttons) that need to preserve the current query when
// changing a single param. The template uses it as:
//
//	href="?{{queryString .Query}}&page=2"
//
// (or similar). The leading "?" is always present (even
// if queryString returns ""), so the link is always a
// valid relative URL.
//
// Per user request 2026-06-27: the pagination links and
// sort-by filter links were not preserving the active
// type filter, search query, or other params — clicking
// "Next" would lose the filter state. This helper makes
// it trivial to preserve them everywhere.
func queryString(query url.Values) template.URL {
	if len(query) == 0 {
		return ""
	}
	var parts []string
	// Iterate in sorted key order for stable test assertions
	// (and predictable HTML output). The test assertions look
	// for strings like "order=desc&page=2&sort=mtime".
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range query[k] {
			// HTMLEscape key+value to prevent XSS via crafted
			// URL params. Then wrap in template.URL so the
			// template engine doesn't double-escape the &, =, etc.
			parts = append(parts, template.HTMLEscaper(k)+"="+template.HTMLEscaper(v))
		}
	}
	return template.URL(strings.Join(parts, "&"))
}

// queryWith returns url.Values with the given key replaced
// (or removed if value is ""). Returns a NEW map (the
// original is not modified). Helper for building URLs
// with overrides; e.g. for the pagination:
//
//	{{range .PageNumbers}}
//	<a href="?{{queryString (queryWith .Query "page" .)}}">{{.}}</a>
//
// Less convenient than queryString for templates (which
// prefer strings) but useful for chained logic in Go.
// Most call sites should use queryString directly.
func queryWith(query url.Values, key, value string) url.Values {
	out := make(url.Values, len(query)+1)
	for k, vs := range query {
		newVs := make([]string, len(vs))
		copy(newVs, vs)
		out[k] = newVs
	}
	if value == "" {
		out.Del(key)
	} else {
		out.Set(key, value)
	}
	return out
}

// sortOrder returns the OPPOSITE order of the current sort
// for the given field. If the field isn't the current sort,
// returns "asc" (the default first-click direction).
// Used by the sort bar to render the toggle href.
//
// Examples (assuming current sort is mtime desc):
//
//	sortOrder("mtime", "desc") → "asc"  (clicking mtime again)
//	sortOrder("name",  "desc") → "asc"  (clicking a different field)
//	sortOrder("mtime", "asc")  → "desc" (clicking mtime again)
//
// Replaces the previous inline template logic that was
// broken when wrapped in a function call (the `if` template
// function can't be used inside a function argument list).
func sortOrder(currentField, field, currentOrder string) string {
	if currentField == field && currentOrder == "asc" {
		return "desc"
	}
	return "asc"
}

// sortURL builds the URL query for a sort-toggle link.
// It sets the new sort field+order, resets page to 1
// (changing the sort should go back to the first page),
// and preserves the active URL-only params (type filter,
// search query, page_size).
func sortURL(query url.Values, field, order string) url.Values {
	out := make(url.Values, len(query)+3)
	for k, vs := range query {
		newVs := make([]string, len(vs))
		copy(newVs, vs)
		out[k] = newVs
	}
	out.Set("sort", field)
	out.Set("order", order)
	out.Del("page")
	return out
}

// queryForPage builds the URL query for a pagination link
// that navigates to a specific page. It preserves the
// EFFECTIVE sort/order (from the Sort field, which has
// defaults applied) and the active URL-only params (type
// filter, search query, page_size). The "page" key is
// replaced (or added) with the new value.
//
// Use this instead of queryWith for pagination links — the
// difference is that queryWith only looks at the URL query
// (so it would lose sort/order when they're at their
// defaults), while queryForPage also includes the effective
// sort/order.
func queryForPage(query url.Values, sort SortSpec, page int) url.Values {
	out := make(url.Values, len(query)+3)
	for k, vs := range query {
		newVs := make([]string, len(vs))
		copy(newVs, vs)
		out[k] = newVs
	}
	out.Set("sort", sort.Field)
	out.Set("order", sort.Order)
	out.Set("page", strconv.Itoa(page))
	return out
}

// queryToHiddenInputs renders url.Values as hidden <input>
// elements, one per value. Used by the page-size form so the
// form preserves other URL parameters (sort, filter, page,
// etc.) when submitting. Skips the page_size key (the
// dropdown itself supplies that).
//
// Convenience wrapper around queryToHiddenInputsExcluding.
// Kept for backward compatibility with existing templates.
func queryToHiddenInputs(query url.Values) template.HTML {
	return queryToHiddenInputsExcluding(query, "page_size")
}

// queryToHiddenInputsExcluding renders url.Values as hidden
// <input> elements, skipping the listed keys. The exclude
// list is variadic so callers can omit any number of keys.
//
// Per user request 2026-06-27: the page-size form uses
// this helper with "page" in the exclude list — changing
// the page size always resets the visitor to page 1 (the
// current page number might not exist after the size
// change).
func queryToHiddenInputsExcluding(query url.Values, exclude ...string) template.HTML {
	// Build a set of excluded keys for O(1) lookup.
	excluded := make(map[string]bool, len(exclude))
	for _, k := range exclude {
		excluded[k] = true
	}
	var buf strings.Builder
	for k, vs := range query {
		if excluded[k] {
			continue
		}
		for _, v := range vs {
			kEsc := template.HTMLEscaper(k)
			vEsc := template.HTMLEscaper(v)
			fmt.Fprintf(&buf, `<input type="hidden" name="%s" value="%s">`, kEsc, vEsc)
		}
	}
	return template.HTML(buf.String())
}

// parseTypeFilter returns the set of file extensions to show.
// It accepts TWO formats (the bookmarkable URL and the standard
// form-submission format):
//
//  1. ?type=jpg,png          (comma-separated, bookmarkable)
//  2. ?ext=jpg&ext=png       (standard form submission)
//
// If both are present, ?type= takes precedence (the
// bookmarkable form is canonical). Each entry is normalised:
// trimmed, lowercased, given a leading dot if missing
// ("jpg" -> ".jpg"). Empty entries (from stray double commas)
// are silently skipped.
//
// Returns nil when no filter is active (no ?type= param, or
// the value is empty / whitespace). A nil return means
// "show all files"; an empty-but-non-nil map (all entries
// filtered out) means "show nothing" (the filter UI will show
// the empty state).
//
// The returned map has no inherent meaning of "image" vs
// "video" — it's just a set of extensions. Callers use it
// with a []FileInfo to keep only files whose ext is in the
// set. Kind-level filtering (image / video) is derived from
// the file's Kind field, not from this set.
func parseTypeFilter(q url.Values) map[string]bool {
	// Prefer ?type= (bookmarkable) over ?ext= (form).
	raw := q.Get("type")
	if strings.TrimSpace(raw) == "" {
		// Fall back to ?ext= (form-submission format: ?ext=jpg&ext=png).
		// url.Values.Get returns the FIRST value of the first key;
		// we need to enumerate all "ext" entries.
		if exts, ok := q["ext"]; ok && len(exts) > 0 {
			raw = strings.Join(exts, ",")
		}
	}
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make(map[string]bool, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		p = strings.ToLower(p)
		if !strings.HasPrefix(p, ".") {
			p = "." + p
		}
		out[p] = true
	}
	return out
}

// typeFilterActive returns true if the query has a ?type= param
// that is non-empty (so a UI can show the "filtered" badge).
// It's a separate helper from parseTypeFilter so the template
// (or other UI code) can check the active state without
// inspecting the parsed map.
func typeFilterActive(q url.Values) bool {
	return strings.TrimSpace(q.Get("type")) != ""
}

// applyTypeFilter returns a copy of files with only the entries
// whose extension is in filter. If filter is nil, the original
// slice is returned unchanged. Empty filter (non-nil but no
// entries) returns an empty slice.
//
// The returned slice shares the underlying FileInfo structs
// with the input — we don't deep-copy. (Callers don't mutate
// the structs after this point, and the cache re-creates them
// from disk each time anyway.)
// applyTypeFilter filters the file list by the active
// ?type= filter. Directories ALWAYS pass through unchanged
// (per user request 2026-06-27: "the directory listing
// should always show"). The filter only affects files.
//
// An empty filter (filter == nil OR len(filter) == 0)
// means "no filter is active" — return all files including
// directories. The old code returned files[:0] on an empty
// filter, which incorrectly hid the directories.
func applyTypeFilter(files []FileInfo, filter map[string]bool) []FileInfo {
	if filter == nil || len(filter) == 0 {
		return files
	}
	out := make([]FileInfo, 0, len(files))
	for _, f := range files {
		// Directories always pass through (the visitor should
		// always be able to navigate up/down even when a file
		// type filter is active).
		if f.Kind == KindDir {
			out = append(out, f)
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.Name))
		if filter[ext] {
			out = append(out, f)
		}
	}
	return out
}

// parseIntDefault is a tiny strconv helper that returns the default
// on parse failure.
func parseIntDefault(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return def, nil
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// const pageSize = 50 has been removed — PageSize is now a
// configuration field on Gallery (see the Caddyfile `page_size`
// directive and the JSON `page_size` field). The default of 50
// is applied in Gallery.Provision when the field is 0.

// FilterOption is one checkbox in the filter UI — represents
// a single file extension (e.g. ".jpg") within a filter group
// (Images, Videos, or Other). The count is the number of files
// in the current directory with this extension; Selected is
// true if the extension is in the active ?type= filter.
type FilterOption struct {
	Ext        string // lowercase, with leading dot, e.g. ".jpg"
	DisplayExt string // canonical-case form for display, e.g. "JPG" (vs "jpg")
	Count      int    // files in the current directory with this ext
	Selected   bool   // currently in the active ?type= filter
}

// FilterGroup is one dropdown in the filter UI (Images, Videos,
// or Other). It has a label, the count of selected sub-types
// vs the total (for the (N/M) chip), and the list of sub-type
// options.
type FilterGroup struct {
	Label    string // "Images", "Videos", "Other"
	Options  []FilterOption
	Selected int // number of Options with Selected=true
	Total    int // len(Options)
}

// computeFilterGroups scans the file list and groups the
// extensions into three filter groups (Images, Videos, Other).
// Each group lists the extensions present in the directory
// with their counts, marking the ones currently in the active
// filter as Selected.
//
// The imageExts and videoExts maps come from the Gallery's
// config (defaultImageExts / defaultVideoExts if not
// overridden). The active filter is the set of extensions
// currently selected (?type= query param).
func computeFilterGroups(files []FileInfo, imageExts, videoExts, activeFilter map[string]bool) (images, videos, other FilterGroup) {
	// Three maps keyed by lowercase ext (with leading dot).
	// Each maps ext -> (count, displayExt). displayExt is the
	// canonical-case form — for the first file we see with
	// that ext, we use whatever case the file actually used
	// (so the dropdown shows "JPG" if the file is "photo.JPG"
	// and "jpg" if it's "photo.jpg").
	imgCounts := map[string]struct {
		count      int
		displayExt string
	}{}
	vidCounts := map[string]struct {
		count      int
		displayExt string
	}{}
	otherCounts := map[string]struct {
		count      int
		displayExt string
	}{}

	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f.Name))
		if ext == "" {
			continue
		}
		switch {
		case imageExts[ext]:
			e := imgCounts[ext]
			e.count++
			if e.displayExt == "" {
				e.displayExt = filepath.Ext(f.Name)
			}
			imgCounts[ext] = e
		case videoExts[ext]:
			e := vidCounts[ext]
			e.count++
			if e.displayExt == "" {
				e.displayExt = filepath.Ext(f.Name)
			}
			vidCounts[ext] = e
		default:
			e := otherCounts[ext]
			e.count++
			if e.displayExt == "" {
				e.displayExt = filepath.Ext(f.Name)
			}
			otherCounts[ext] = e
		}
	}

	// Convert maps to sorted slices. Sort alphabetically by
	// displayExt so the dropdown is predictable.
	images = filterGroupFromMap("Images", imgCounts, activeFilter)
	videos = filterGroupFromMap("Videos", vidCounts, activeFilter)
	other = filterGroupFromMap("Other", otherCounts, activeFilter)
	return
}

func filterGroupFromMap(label string, counts map[string]struct {
	count      int
	displayExt string
}, activeFilter map[string]bool) FilterGroup {
	out := FilterGroup{Label: label}
	for ext, info := range counts {
		out.Options = append(out.Options, FilterOption{
			Ext:        ext,
			DisplayExt: info.displayExt,
			Count:      info.count,
			Selected:   activeFilter[ext],
		})
	}
	sort.Slice(out.Options, func(i, j int) bool {
		return out.Options[i].DisplayExt < out.Options[j].DisplayExt
	})
	for _, o := range out.Options {
		out.Total++
		if o.Selected {
			out.Selected++
		}
	}
	return out
}

// RenderPage renders the gallery page for a directory. The caller
// provides the raw directory listing (output of Scanner.Scan);
// RenderPage does the split / sort / paginate / format work.
//
// `title` is the page heading (e.g. "Generated Images"). It is
// derived by the caller — typically the basename of the current
// directory.
//
// `pathPrefix` and `thumbPrefix` are the URL prefixes for
// relative links. The defaults used in the live config are "./"
// and "./_thumbs/" — both relative so they work for any subdir
// the gallery is mounted at.
//
// `relPath` is the path within the gallery (the request's
// post-handle_path-stripped path, no leading slash). Empty for
// the gallery root. When non-empty, an ".." entry is prepended to
// the directories list so the user can navigate up.
//
// `query` is the request's URL query values; sort and page are
// read from it.
// RenderPage renders the gallery. `tmplName` is the configured
// template name (relative to the templates dir). Pass "" to use
// the default ("gallery.tmpl"). The name is validated inside
// loadTemplate.
// `noThumbs` is the configured no_thumbs flag - when true,
// image tiles use the original file as the <img src> instead
// of /_thumbs/<name>.webp (no thumb generation).
// `pageSize` is the configured page_size - the number of image
// entries per page. Pass 0 for the default of 60 (per user
// request 2026-06-20).
// `pageSizes` is the list of per-page options the visitor can
// choose from in the dropdown (e.g. [30, 60, 120, "all"]).
// "all" is a special token meaning "show all items on one
// page" - only included if the operator explicitly listed it.
// Default: [30, 60, 120, "all"].
// `imageExts` and `videoExts` are the Gallery's configured
// extension sets (used to build the filter UI's sub-type groups).
// `breadcrumbRoot` is the gallery's URL mount prefix (e.g.
// "images" for /images/*) - used as the first segment of the
// breadcrumb. `absolutePrefix` is the absolute URL path (e.g.
// "/images/") - used as the prefix for absolute breadcrumb
// links.
func RenderPage(title, pathPrefix, thumbPrefix, relPath, tmplName string, noThumbs, noVideoThumbs bool, pageSize int, pageSizes []string, files []FileInfo, query url.Values, imageExts, videoExts map[string]bool, breadcrumbRoot, absolutePrefix string) (string, error) {
	sortSpec := parseSort(query)
	page := pageFromQuery(query)
	// Per user request 2026-06-27: read ?page_size=N from the
	// URL so the visitor's dropdown selection takes effect.
	// pageSizeFromQuery returns -1 if not specified (caller
	// then uses the operator-configured default). The "all"
	// token is converted to 0 (which means "no pagination"
	// downstream — paginate() returns all items).
	if ps := pageSizeFromQuery(query); ps >= 0 {
		pageSize = ps
	}

	// Per user request 2026-06-20: compute the filter UI
	// data BEFORE applying the filter, so the dropdowns show
	// all available sub-types (not just the currently-visible
	// ones). The user might want to switch from "jpg" to "png"
	// and we should show them "png" exists in the dropdown.
	typeFilter := parseTypeFilter(query)
	imgGroup, vidGroup, otherGroup := computeFilterGroups(
		files, imageExts, videoExts, typeFilter,
	)

	// Apply the ?q= search filter to the file list BEFORE
	// splitFiles. Only files (not directories) are searched —
	// a search should still show the user the directories they
	// can navigate to. Directories pass through unchanged.
	searchQuery := parseSearchQuery(query.Get("q"))
	files = applySearchFilter(files, searchQuery)

	// Apply the ?type= filter to the file list BEFORE
	// splitFiles. Per user request 2026-06-27: directories
	// ALWAYS pass through unchanged (the dirs table should
	// always show, even when a filter is active — the visitor
	// should be able to navigate the directory tree regardless
	// of what file-type filter is active). applyTypeFilter
	// implements this: it checks the Kind field and skips
	// directories, so only files are filtered by the extension.
	files = applyTypeFilter(files, typeFilter)

	dirs, others, allImages := splitFiles(files)
	sortFiles(allImages, sortSpec)
	// Per user request 2026-06-20: "other files should respond
	// to the sorting" — sort them by the same sort spec the
	// user picked for the image grid. The dirs are NOT sorted
	// here (splitFiles keeps them alphabetical).
	sortFiles(others, sortSpec)
	if pageSize <= 0 {
		pageSize = 60
	}
	// Per user request 2026-06-20: validate the requested page
	// size against the operator-configured pageSizes list. If
	// the requested value is not in the list, fall back to the
	// first item in the list (e.g. 30 if [30, 60, 120, "all"]
	// is configured).
	pageSize = validatePageSize(pageSize, pageSizes)
	paged := paginate(allImages, page, pageSize)
	totalImages := len(allImages)
	// ImageStart/ImageEnd are the 1-based range of images shown
	// on the current page. For page 1, this is 1..N. For
	// subsequent pages, it continues from the end of the
	// previous page. If there are no images, both are 0.
	// Per user request 2026-06-27: the media header shows
	// "Media (TotalImages - Showing ImageStart-ImageEnd)".
	var imageStart, imageEnd int
	if totalImages > 0 {
		imageStart = (page-1)*pageSize + 1
		imageEnd = imageStart + len(paged) - 1
	}
	// Split the media count by type so the header meta line
	// can show "N images" (actual image count) and "M videos"
	// (video count) separately. Per user request 2026-06-17:
	// videos were previously miscounted as images in the
	// "X images" label.
	imageCount := 0
	videoCount := 0
	var totalAllBytes int64
	for _, f := range allImages {
		if f.Kind == KindVideo {
			videoCount++
		} else {
			imageCount++
		}
		totalAllBytes += f.Size
	}
	// Per user request 2026-06-18 (Phase 44): the size shown in
	// the header is the TOTAL of ALL files (images + videos +
	// other files), not just images or just other files. Excludes
	// subdirectories.
	for _, f := range others {
		totalAllBytes += f.Size
	}
	totalPages := (totalImages + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}

	// Split dirs into Up (the synthetic ../ entry, only present
	// in subdirs) and Subdirs (the actual subdirs). The Up
	// entry is rendered on its own line (always first when
	// present); Subdirs is rendered in a tight row with no
	// gap between chips, per the user's 2026-06-17 spec.
	subdirViews := buildFileViews(dirs, pathPrefix, thumbPrefix, noThumbs, noVideoThumbs)
	var up *FileView
	if relPath != "" {
		// Compute the parent directory's basename so the up
		// chip can show "Up (../{name})". E.g. when viewing
		// "/photos/vacation/", the parent dir's name is
		// "photos" and the chip reads "Up (../photos)". Empty
		// at the gallery root or in a top-level subdir
		// (parent is the gallery root, no name to show).
		//
		// Trim any trailing slash first: when the URL is
		// "/images/photos/", relPath is "photos/" (with
		// trailing slash from the URL). filepath.Dir("photos/")
		// returns "photos" (filepath.Clean strips the slash
		// before splitting), which is the CURRENT dir's name,
		// not the parent's. Without the trim, the chip would
		// say "Up (../photos)" while the user is in
		// /images/photos/ — same text as the current dir.
		cleanPath := strings.TrimSuffix(relPath, "/")
		parentDir := ""
		if pd := filepath.Base(filepath.Dir(cleanPath)); pd != "." {
			parentDir = pd
		}
		up = &FileView{
			Name:      "Up",
			Href:      "../",
			IsDir:     true,
			IsUp:      true,
			ParentDir: parentDir,
		}
	}

	data := PageData{
		Title:       title,
		PathPrefix:  pathPrefix,
		ThumbPrefix: thumbPrefix,
		Up:          up,
		Subdirs:     subdirViews,
		OtherFiles:  buildFileViews(others, pathPrefix, thumbPrefix, noThumbs, noVideoThumbs),
		Images:      buildFileViews(paged, pathPrefix, thumbPrefix, noThumbs, noVideoThumbs),
		Page:        page,
		PageSize:    pageSize,
		PageSizes:   pageSizes,
		Query:       query,
		SearchQuery: query.Get("q"),
		TotalImages: totalImages,
		ImageCount:  imageCount,
		ImageStart:  imageStart,
		ImageEnd:    imageEnd,
		TotalVideos: videoCount,
		// Per user request 2026-06-19: pre-compute the total
		// number of files (images + videos + other files) for
		// the "N files" label at the start of the meta line.
		// Doing this in Go (vs in the template) avoids needing
		// an `add` template function.
		TotalFiles:         imageCount + videoCount + len(others),
		TotalAllFilesSize:  humanSize(totalAllBytes),
		TotalPages:         totalPages,
		HasPrev:            page > 1,
		HasNext:            page < totalPages,
		PageNumbers:        pageNumbers(page, totalPages),
		Sort:               sortSpec,
		Breadcrumb:         computeBreadcrumb(relPath, title, pathPrefix, breadcrumbRoot, absolutePrefix),
		TypeFilter:         typeFilter,
		IsTypeFilterActive: typeFilterActive(query),
		TypeFilterQuery:    strings.TrimSpace(query.Get("type")),
		FilterImageOptions: imgGroup,
		FilterVideoOptions: vidGroup,
		FilterOtherOptions: otherGroup,
	}

	tmpl, err := loadTemplate(tmplName)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// buildFileViews maps a []FileInfo to a []FileView.
func buildFileViews(files []FileInfo, pathPrefix, thumbPrefix string, noThumbs, noVideoThumbs bool) []FileView {
	out := make([]FileView, 0, len(files))
	for _, f := range files {
		out = append(out, buildFileView(f, pathPrefix, thumbPrefix, noThumbs, noVideoThumbs))
	}
	return out
}

// galleryTemplate is the complete HTML for the gallery page with
// the CSS and JS inlined as <style> and <script> blocks. Keeping
// everything in a single Go string constant (and a single on-disk
// file) makes the template easier to edit and read — the operator
// can scan the whole page top to bottom in one place, with the
// CSS rules interleaved with the HTML they apply to and the JS
// at the bottom.
//
// The single template is parsed by html/template. html/template
// uses the same {{...}} syntax for both variable substitution and
// control flow (if, range, with, end), so be careful when
// writing raw CSS like `width: {{.Width}}` — it WILL be
// auto-escaped. The `template "name" .` sub-template references
// have been removed (the inlining makes them unnecessary).
//
// Data passed to this template: see PageData in this file, plus
// the funcs in galleryFuncs (minus1, plus1, sortLabel). The
// per-tile FileView fields are documented in docs/templates.md
// in this repo.
const galleryTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}}</title>
  <!-- No-flash-of-wrong-theme: read the visitor's saved
       preference from localStorage and apply it to <html>
       BEFORE the page paints. This runs synchronously in the
       <head> so the CSS already sees the correct data-theme
       attribute when the body renders. Without this, a visitor
       who picked "Dark" would briefly see a flash of light
       theme on each page load before the JS at the bottom of
       the page applies the saved choice. -->
  <script>
    (function() {
      try {
        var t = localStorage.getItem('gallery-theme');
        if (t === 'light' || t === 'dark') {
          document.documentElement.setAttribute('data-theme', t);
        }
      } catch (e) { /* localStorage unavailable */ }
    })();
  </script>
<style>

* { box-sizing: border-box; margin: 0; padding: 0; }
/* === COLOR TOKENS ============================================
   Defined as CSS custom properties so dark mode is a single
   override block, not a CSS duplication exercise. The light
   values are the defaults; dark overrides come via either the
   prefers-color-scheme media query (auto) or [data-theme="dark"]
   (manual override via the in-page toggle, persists in
   localStorage). */
:root {
  --bg: #f3f6f7;             /* page background */
  --bg-card: #ffffff;        /* card/chip background (sort UI, page-btn, dirs-row) */
  --bg-chip: #f3f6f7;        /* subtle chip background (dirs, sort indicator, theme toggle) */
  --bg-hover: #e5e9ea;       /* hover background for chips */
  --bg-active: #f3f6f7;      /* active/selected chip background */
  --fg: #111111;             /* primary text */
  --fg-muted: #666666;       /* secondary text (meta, sort labels) */
  --fg-faint: #888888;       /* tertiary text (page-info) */
  --fg-disabled: #bbbbbb;    /* disabled text (page-btn.disabled) */
  --border: #e5e9ea;         /* standard border */
  --border-strong: #d0d4d6;  /* hover border */
  --accent: #006ed3;         /* accent color (links, card hover border) */
  --accent-hover: #0095e4;   /* accent hover */
  --accent-bg: #006ed3;      /* button-background accent (active sort/page btns); darker in dark mode to fit the aesthetic */
  /* Per user request 2026-06-19: the active sort + pagination
     buttons no longer use the blue --accent-bg. Instead they
     use the OPPOSITE mode's page background (and foreground)
     for a color-contrast inversion:
       - Light mode active button: dark bg + light fg (the
         dark mode's page colors)
       - Dark mode active button: light bg + dark fg (the
         light mode's page colors)
     This makes the active button stand out by being visually
     inverted from the page, rather than by being blue (which
     felt too vibrant). The tokens are scoped to the active
     state, so the rest of the UI is unaffected. */
  --active-bg: #1a1a1a;      /* active button bg in light mode = dark mode's --bg */
  --active-fg: #e5e5e5;      /* active button fg in light mode = dark mode's --fg */
  --active-border: #1a1a1a;  /* active button border in light mode = matches --active-bg */
  --shadow: rgba(0, 0, 0, 0.05);  /* standard shadow */
  --shadow-strong: rgba(0, 0, 0, 0.15); /* strong shadow (lightbox) */
}
/* Auto dark mode: applies when the visitor's OS is in dark mode,
   UNLESS they have explicitly chosen "light" (overrides the media
   query). The :not([data-theme="light"]) selector is the trick
   that makes "Auto" mode work: when the OS is dark but the user
   picked Light, this block doesn't apply. */
@media (prefers-color-scheme: dark) {
  :root:not([data-theme="light"]) {
    --bg: #1a1a1a;
    --bg-card: #252525;
    /* chip bg in dark mode is intentionally the SAME as the
       page bg (not lighter) so chips don't stand out as a
       visible element — only the border + text show. This
       mirrors the light-mode behavior (--bg-chip = --bg in
       light mode too) and matches how the .tile-name /
       .tile-meta elements look on the card (text directly on
       the card bg, no separate fill). Per user feedback
       2026-06-18, even the previous #1d1d1d was 'too light
       for the page'; the user wants chips to blend in like
       the tile details. */
    --bg-chip: #1a1a1a;
    --bg-hover: #333333;
    --bg-active: #2a2a2a;
    --fg: #e5e5e5;
    --fg-muted: #aaaaaa;
    --fg-faint: #888888;
    --fg-disabled: #666666;
    --border: #333333;
    --border-strong: #444444;
    --accent: #4dabff;
    --accent-hover: #6b9fd8;
    /* accent-bg in dark mode is intentionally darker than --accent
       (which is light blue for text + borders). A bright accent as
       a button fill on a dark page looks glaring; the muted
       #3b6fb6 is more in keeping with the dark aesthetic. The
       active sort/page buttons use --accent-bg (not --accent)
       for their fill. */
    --accent-bg: #3b6fb6;
    /* Per Phase 85: the active sort + pagination buttons in
       dark mode use the LIGHT mode's page bg + fg (color
       inversion from the page). The user wanted the active
       button to NOT be blue. The active button in dark mode
       now stands out by being a light element on a dark page
       (inversion), rather than by being a colored (blue) accent. */
    --active-bg: #f3f6f7;      /* active button bg in dark mode = light mode's --bg */
    --active-fg: #111111;      /* active button fg in dark mode = light mode's --fg */
    --active-border: #f3f6f7;  /* active button border in dark mode = matches --active-bg */
    --shadow: rgba(0, 0, 0, 0.3);
    --shadow-strong: rgba(0, 0, 0, 0.5);
  }
}
/* Manual dark override: applies regardless of OS preference.
   Triggered by clicking the moon icon in the header; the choice
   is stored in localStorage and read on page load. */
[data-theme="dark"] {
  --bg: #1a1a1a;
  --bg-card: #252525;
  /* Same as @media (prefers-color-scheme: dark): chips blend
     into the page, only border + text are visible. See comment
     in the @media block above for the rationale. */
  --bg-chip: #1a1a1a;
  --bg-hover: #333333;
  --bg-active: #2a2a2a;
  --fg: #e5e5e5;
  --fg-muted: #aaaaaa;
  --fg-faint: #888888;
  --fg-disabled: #666666;
  --border: #333333;
  --border-strong: #444444;
  --accent: #4dabff;
  --accent-hover: #6b9fd8;
  --accent-bg: #3b6fb6;
  /* Per Phase 85: same as the @media (prefers-color-scheme: dark)
     block — the active button inverts the page contrast (light
     bg + dark fg in dark mode). */
  --active-bg: #f3f6f7;
  --active-fg: #111111;
  --active-border: #f3f6f7;
  --shadow: rgba(0, 0, 0, 0.3);
  --shadow-strong: rgba(0, 0, 0, 0.5);
}

/* === THEME TOGGLE BUTTON GROUP (header) ====================== */
.theme-toggle {
  display: inline-flex;
  gap: 2px;
  padding: 3px;
  background: var(--bg-chip);
  border: 1px solid var(--border);
  border-radius: 6px;
}
.theme-toggle button {
  background: transparent;
  border: 0;
  padding: 0.3rem 0.55rem;
  border-radius: 4px;
  cursor: pointer;
  color: var(--fg-muted);
  font-size: 0.95rem;
  line-height: 1;
  font-family: inherit;
  transition: background 0.12s, color 0.12s;
}
.theme-toggle button:hover {
  background: var(--bg-hover);
  color: var(--fg);
}
.theme-toggle button[aria-pressed="true"] {
  background: var(--bg-card);
  color: var(--fg);
  border: 1px solid var(--border);
  box-shadow: 0 1px 2px var(--shadow);
}

html, body { background: var(--bg); color: var(--fg); }
body {
  font-family: Inter, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, system-ui, sans-serif;
  font-size: 16px;
  line-height: 1.5;
  text-rendering: optimizeLegibility;
  min-height: 100vh;
  padding: 2rem 1rem 4rem;
}
a { color: var(--accent); text-decoration: none; }
a:hover { color: var(--accent-hover); }
main {
  max-width: 1200px;
  margin: 0 auto;
  background: var(--bg-card);
  border-radius: 5px;
  box-shadow: 0 2px 5px 1px var(--shadow);
  overflow: hidden;
}
header {
  padding: 1.25rem 2rem 0;
  /* Per user request 2026-06-19: removed border-bottom from the
     <header>. The line that separates the header content (title +
     meta + sort-bar) from the rest of the page is now the
     sort-bar's border-bottom, which extends to the viewport
     edges via negative horizontal margin (-2rem to escape the
     header's 2rem padding). This makes the line under the sort
     buttons the same width as the lines under the section
     headings and the section content (all extend to the
     viewport edges). */
}
.header-top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
  /* Per user request 2026-06-19: removed margin-bottom: 0.85rem.
     Added a border-bottom + padding-bottom to create a
     visible separator between the title/meta row and the
     sort-bar row below. The 1px line extends the full width
     of the .header (no negative margin needed because the
     .header has padding 0 2rem 0, and the .header-top is
     full-width inside that padding). */
  border-bottom: 1px solid var(--border);
  padding-bottom: 0.75rem;
}
.header-main { flex: 1 1 auto; min-width: 0; }
h1 {
  font-size: 1.5rem;
  font-weight: 600;
  color: var(--fg);
  margin-bottom: 0.25rem;
}
.meta {
  font-size: 0.875rem;
  color: var(--fg-muted);
  display: flex;
  gap: 0.5rem;
  flex-wrap: wrap;
}
.meta span { color: var(--fg-faint); }
.sort-indicator {
  flex: 0 0 auto;
  align-self: flex-start;
  margin-top: 0.3rem;
  font-size: 0.8rem;
  color: var(--fg);
  text-decoration: none;
  white-space: nowrap;
  transition: background 0.12s, border-color 0.12s;
}
a.sort-indicator:hover { background: var(--bg-hover); border-color: var(--border-strong); color: #006ed3; }
/* Per user request 2026-06-18: arrows in the media gallery
   should be the same color in both light and dark modes.
   Fixed #006ed3 (the light-mode accent) — a medium blue
   that's visible on both light and dark page backgrounds
   (decent contrast either way). Phase 48 made the .sort-indicator
   hover state fixed; this makes the NORMAL state of the arrows
   fixed too, matching the user's spec. */
.sort-indicator .arrow { margin-left: 0.3rem; font-weight: 600; color: #006ed3; }
.section {
  padding: 1.25rem 2rem;
  border-bottom: 1px solid var(--border);
}
.section:last-child { border-bottom: none; }
.section-heading {
  /* Per user request 2026-06-19: bumped from 0.7rem to 0.85rem —
     the section headings (DIRECTORIES, OTHER FILES, MEDIA) were
     too small relative to the body text. 0.85rem matches the
     meta line size and is more readable. */
  font-size: 0.85rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--fg-muted);
  margin-bottom: 0.75rem;
  /* The section heading is a flex container so the title text
     and the toggle button can sit on the same row. The title
     text gets the bulk of the space; the toggle sits at the far
     right. A horizontal rule (per Phase 72) sits between them
     at ~50% text height, visually connecting the section name
     to its toggle. */
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  /* Per user request 2026-06-19: the whole heading row is
     clickable (not just the small button). The padding +
     cursor: pointer + hover bg show the user this is
     interactive. We use cursor: pointer (not a cursor on the
     button only) because the entire row is the click target. */
  cursor: pointer;
  padding: 0.25rem 0.5rem;
  margin-left: -0.5rem;
  margin-right: -0.5rem;
  border-radius: 4px;
  transition: background 0.12s;
  user-select: none;
}
.section-heading:hover { background: var(--bg-hover); }
/* Per user request 2026-06-19: a horizontal rule between the
   section name and the toggle button. The rule sits at roughly
   50% of the text height (the lowercase x-height) and stretches
   to fill the gap. It's a thin (1px) line in the muted border
   color so it doesn't dominate the heading — it's a visual
   link, not a divider. */
.section-heading .heading-divider {
  /* Per user request 2026-06-20 (Phase 108): the line between
     the section name and the toggle button was getting
     "lost" (effectively invisible) because:
     (a) the flex container with justify-content: space-between
         was squeezing it, and
     (b) the 1px line in --border color is subtle by design.
     We fix both:
     - flex: 1 1 0 with a min-width of 6rem so the line
       always has room to be visible.
     - 2px height + var(--border-strong) (slightly darker
       than --border) for better contrast.
     The result is a clear horizontal rule between the section
     name (e.g. "DIRECTORIES (3)") and the toggle button
     (e.g. "−"). */
  flex: 1 1 0;
  min-width: 6rem;
  height: 2px;
  background: var(--border-strong);
  align-self: center;
  margin: 0 0.5rem;
  border-radius: 1px;
}
/* Per Phase 71: the section-toggle button lets the visitor
   collapse the directories + other-files sections. Default
   state is expanded (the body is shown); clicking the button
   hides the body. The state is persisted in localStorage
   (per-section key) so the visitor's choice survives
   navigations and refreshes. */
.section-toggle {
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 4px;
  color: var(--fg-muted);
  font-size: 0.9rem;
  font-weight: 700;
  line-height: 1;
  width: 1.5rem;
  height: 1.5rem;
  cursor: pointer;
  display: inline-flex;
  /* Per user request 2026-06-19: removed align-items: center.
     The button is a fixed-size square (1.5rem x 1.5rem) and
     contains a single character (− or +) with line-height: 1.
     The center alignment was making the character sit at the
     vertical middle of the line box, which can look slightly
     off because the character's optical center isn't always
     at the line box center. Without align-items: center,
     the character uses its natural baseline (which is
     tighter and usually looks better for single-character
     buttons). */
  justify-content: center;
  transition: background 0.12s, color 0.12s, border-color 0.12s;
  font-family: inherit;
  flex-shrink: 0;
}
.section-toggle:hover {
  background: var(--bg-hover);
  color: var(--fg);
  border-color: var(--border-strong);
}
.section-toggle:focus {
  outline: 2px solid #006ed3;
  outline-offset: 1px;
}
/* When the section is collapsed (set via the JS class toggle),
   the body is hidden and the section has a smaller bottom
   margin (no point reserving space for hidden content). */
.dirs-section.collapsed,
.others-section.collapsed,
.media-section.collapsed {
  margin-bottom: 0;
  padding-bottom: 0.5rem;
}
.dirs-section.collapsed .section-body,
.others-section.collapsed .section-body,
.media-section.collapsed .section-body {
  display: none;
}
.chip-row {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
/* Dirs section layout (Phase 24, per user request 2026-06-17):
   - Up chip is rendered on its OWN LINE, always first
   - The dirs section is only shown if there's an Up entry
     OR at least one subdir */
/* Per user request 2026-06-20: subdirs are now a full-width
   table (see .files-table below), not a chip row. The Up chip
   remains above the table as a quick "go up" affordance.
   The .dirs-row and .dirs-section .dirs-row classes are kept
   here as no-ops (replaced by .files-table) so old overrides in
   custom templates don't break — they just don't do anything. */
.dirs-section .up-chip-row {
  margin-bottom: 0.5rem; /* visual separation from the subdirs below */
}
/* Per user request 2026-06-19: the Up entry is now rendered
   as a SEPARATE TABLE above the dirs table (not as a row
   inside the dirs table). This avoids the up-spacer-row that
   used to highlight on hover. The up-row-table has no
   <thead> (it's just one row with a colspan=3 cell) and no
   <tbody> hover behavior (the .files-table tr:hover rule
   only matches .files-table, not .up-row-table).

   Visual separation comes from:
   - the up-row-table's own bottom border
   - the natural margin between the two tables

   The link inside the table cell is styled like the old
   up-row's link (inherits --fg color, accent on hover). */
.up-row-table {
  width: 100%;
  border-collapse: collapse;
  margin-bottom: 0.25rem; /* small gap before the dirs-table */
  /* Per user request 2026-06-19: added font-size: 0.85rem to
     match .files-table. The up-row-table didn't have a
     font-size override, so it inherited the default
     (typically 1rem / 16px), which made the Up link text
     BIGGER than the other directory rows in the dirs
     table below (which use 0.85rem via .files-table).
     Now both use 0.85rem, so the text is the same size. */
  font-size: 0.85rem;
}
.up-row-table td {
  background: var(--bg-card);
  border-bottom: 1px solid var(--border);
  padding: 0.5rem 0.75rem;
  /* Per user request 2026-06-19: removed font-weight: 500.
     The up-row's text was bolder (and thus appeared bigger)
     than the other directory rows in the dirs table below.
     Now both use the default font-weight (inherited from
     the .files-table / .up-row-table base, which is
     font-weight: 400 / normal), so the Up link is the
     same text size as the other directories. */
}
.up-row-table a {
  color: var(--fg);
  text-decoration: none;
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
}
.up-row-table a:hover {
  color: var(--accent);
}

/* Full-width tables for directories (Phase 69) and other files.
   Same structure: <table class="files-table dirs-table" id="dirs-table"> or
   <table class="files-table others-table" id="others-table">. The class .files-table
   carries the shared styling (width, borders, hover); the
   .dirs-table / .others-table class can be used for any
   per-section override (currently none).

   The Name column is the only clickable one — the link wraps
   the icon + name. Other cells are non-clickable.

   Why a <table> and not a CSS grid: a real <table> gives us
   accessible semantics for free (screen readers announce
   "table with N rows, columns: Name, Type, Date"), and the
   default behavior (cell padding, alignment) is close to what
   we want. CSS grid would require explicit role="grid" + ARIA
   attributes to be accessible. */
.files-table {
  width: 100%;
  border-collapse: collapse;
  margin-top: 0.5rem;
  font-size: 0.85rem;
}
.files-table thead th {
  text-align: left;
  font-weight: 600;
  color: var(--fg-muted);
  padding: 0.4rem 0.75rem;
  border-bottom: 1px solid var(--border);
  background: var(--bg-card);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  font-size: 0.7rem;
}
.files-table tbody td {
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid var(--border);
  vertical-align: middle;
}
.files-table tbody tr:last-child td {
  border-bottom: none; /* no border on the last row */
}
.files-table tbody tr:hover {
  background: var(--bg-hover);
}
.files-table .col-name {
  /* Name column: takes most of the width; the link inside is
     the primary clickable element. */
  width: auto;
}
.files-table .col-type {
  /* Per user request 2026-06-19: changed width from 6rem
     (fixed narrow) to auto. With the dirs table no longer
     having a Type column (Phase 77), the col-type only
     applies to the others table. The 6rem fixed width was
     fine for short extensions like "TXT" or "PDF" but
     truncated longer ones like "WEBM" or "MARKDOWN".
     Using auto lets the column size to its content (max
     of the header + the data rows). The column will be
     just wide enough to fit the longest extension without
     wasted space. */
  width: auto;
  color: var(--fg-muted);
  font-size: 0.75rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
}
.files-table .col-size {
  /* Size column: narrow, right-aligned, formatted by humanSize()
     (e.g. "1.2 MB", "342 B"). */
  width: 7rem;
  text-align: right;
  color: var(--fg-muted);
  font-variant-numeric: tabular-nums; /* digits line up across rows */
}

/* Per user request 2026-06-27: # Items and # Sub-Dirs
   columns in the dirs table. Right-aligned, tabular-nums
   so the digits line up across rows. Narrow (5rem) since
   these are small integers.

   Per user request 2026-06-27: the column heading
   "#&nbsp;Items" / "#&nbsp;Sub-Dirs" uses non-breaking
   spaces (the entity inside the HTML, not just CSS) so
   the heading never splits across lines. The
   white-space: nowrap rule below is a defensive
   backstop: it prevents the cell content (e.g. long
   counts in the future) from wrapping to a second line,
   which would break the column alignment. */
.col-count {
  width: 5rem;
  text-align: right;
  color: var(--fg-muted);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.files-table .col-date {
  /* Date column: narrow-ish, formatted by formatDate()
     (e.g. "2026-06-20 14:30" or "Yesterday"). */
  width: 11rem;
  color: var(--fg-muted);
  white-space: nowrap;
}

/* Per user request 2026-06-27: clickable column headers for
   the dirs and others tables. The .sortable class adds a
   pointer cursor and a hover effect. The .sort-indicator
   span inside the <th> shows ▲ (asc) or ▼ (desc) for the
   active sort column. Empty when the column is not the
   active sort. */
.sortable {
  cursor: pointer;
  user-select: none;
}
.sortable:hover {
  background: var(--bg-hover);
}
.sortable.sort-active {
  /* Active column header (when a dirs/others table column
     is the current sort key). Per user request 2026-06-27:
     no longer color or bold the active header — the ▲/▼
     glyph in the .sort-indicator span is enough to show
     which column is the active sort. */
}
.sort-indicator {
  display: inline-block;
  width: 0.85rem;
  margin-top: -0.3rem;
  margin-left: 0.25rem;
  font-size: 0.75rem;
  vertical-align: middle;
  /* Per user request 2026-06-27: removed color so the
     ▲/▼ indicator is no longer tinted with the brand
     colour. Inherits the default text colour from the
     <th> (which is the default page colour since the
     active <th> is no longer colored either). */
}
.table-link {
  /* The link inside the Name cell. Inherits color from the
     parent (we don't want a bright blue link color in this
     table — it's a list of files, not a paragraph of prose). */
  color: var(--fg);
  text-decoration: none;
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
}
.table-link:hover {
  color: var(--accent); /* hover state: accent color (slight affordance) */
}
/* Per user request 2026-06-19: the cell-link wraps the content
   of the Type/Size/Date cells (so clicking anywhere in the row
   navigates to the file). It uses display: block + height:
   100% to make the entire <td> clickable, with padding to match
   the cell's own padding. The cell-link inherits the cell's
   text styling (color from .col-type, .col-size, .col-date).
   tabindex="-1" + aria-hidden="true" in the HTML keep this
   invisible to screen readers and the keyboard tab order
   (the only "real" link per row is the Name cell, which is
   what the screen reader announces). */
.files-table .cell-link {
  display: block;
  /* Negative margin compensates for the cell's own padding
     so the link's hit area extends to the cell edges. */
  margin: -0.5rem -0.75rem;
  padding: 0.5rem 0.75rem;
  color: inherit; /* inherit the muted color from .col-type / .col-size / .col-date */
}
.chip {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.4rem 0.75rem;
  background: var(--bg-chip);
  border: 1px solid var(--border);
  border-radius: 4px;
  color: var(--fg);
  font-size: 0.8rem;
  text-decoration: none;
  white-space: nowrap;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  transition: background 0.12s, border-color 0.12s;
}
.chip:hover { background: var(--bg-hover); border-color: var(--border-strong); color: var(--accent); }
.chip-icon { font-size: 0.95rem; line-height: 1; }
.dir-chip {
  font-weight: 500;
  margin-right: 6px;
  margin-bottom: 6px;
}
.media-section { padding: 1.25rem 2rem 1.5rem; }
.dirs-section, .others-section { padding: 1rem 2rem; }
.sort-bar {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  font-size: 0.85rem;
  flex-wrap: wrap;
  /* Per user request 2026-06-19 (Phase 80): removed
     margin: 0 -2rem (the sort-bar no longer needs to
     escape the header's 2rem padding because the visual
     separation above is now handled by .header-top's
     border-bottom). Changed padding from
     '0.75rem 2rem 0' (top 0.75, horizontal 2rem, bottom 0)
     to '0.75rem 0 0.75rem 0' (top 0.75, horizontal 0, bottom
     0.75). The 2rem horizontal was matching the header's
     padding; since the header is now full-width, the
     sort-bar can also be full-width. The bottom padding
     was added so the border-bottom doesn't sit right next
     to the sort buttons. */
  padding: 0.75rem 0 0.75rem 0;
  border-bottom: 1px solid var(--border);
}
.sort-label { color: var(--fg-faint); margin-right: 0.25rem; }
.sort-btn {
  display: inline-flex;
  align-items: center;
  padding: 0.3rem 0.65rem;
  border: 1px solid var(--border);
  border-radius: 4px;
  color: var(--fg);
  text-decoration: none;
  background: var(--bg-card);
  transition: background 0.12s, border-color 0.12s;
}
.sort-btn:hover { background: var(--bg-hover); border-color: var(--border-strong); }
.sort-btn.active {
  /* Per Phase 85: the active sort button no longer uses
     --accent-bg (blue). It uses --active-bg / --active-fg
     / --active-border which are the OPPOSITE mode's page
     colors. So in light mode the active button is dark
     (matching dark mode's --bg) with light text; in dark
     mode it's light (matching light mode's --bg) with dark
     text. The color contrast inversion makes the active
     button stand out without using blue. */
  background: var(--active-bg);
  border-color: var(--active-border);
  color: var(--active-fg);
  font-weight: 500;
}
.sort-btn.active:hover {
  /* Per Phase 85: hover on the active sort button keeps the
     inverted look. We use --active-bg + a small filter to
     darken/lighten it slightly, OR we just use a slightly
     different border. For simplicity we just use the
     --active-border (slightly darker in light mode, slightly
     lighter in dark mode) to give a visual feedback on hover
     without changing the fill. */
  border-color: var(--border-strong);
}
/* Per user request 2026-06-19: the sort-by arrow (↑/↓) is
   too close to the button label ("Name", "Type", etc.).
   Add padding-left to the .arrow span so there's breathing
   room between the label text and the arrow. The arrow's
   color is inherited from the button's color (--active-fg
   on the active button, --fg on inactive buttons). */
.sort-btn .arrow {
  padding-left: 0.3rem;
  font-weight: 600;
}
/* Per user request 2026-06-20 (Phase 3): the breadcrumb
   is a small, unobtrusive row of links below the sort-bar.
   Each segment is a clickable link except the current
   directory (last segment, plain text). The separator is a
   single slash with muted color, matching the file-manager
   convention (rather than the breadcrumb-arrow ">" which
   would compete visually with the sort buttons). The
   whole row wraps on narrow viewports (long path names). */
/* Per user request 2026-06-20: the breadcrumb is rendered
   as a row of rectangular pills (the "chevron" chevron shape
   was removed in favour of a simpler rectangular design — see
   the "show stop the chevrons" user request). Each segment
   is a simple bordered box with the name + a » separator
   between segments. The CURRENT (last) segment is rendered
   as plain text (not a link) with a chevron-pointed right
   edge as the "you are here" indicator.

   Per user request 2026-06-20: padding-bottom: 0.25rem added
   so the breadcrumb-link's own bottom border is NOT cut off
   by the breadcrumb container's bottom border (which is the
   separator line between the breadcrumb and the sort-bar
   below). */
.breadcrumb {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.25rem 0.5rem;
  font-size: 0.85rem;
  padding: 0.75rem 0 0.5rem 0;
  border-bottom: 1px solid var(--border);
}
/* Per user request 2026-06-20: the breadcrumb is now
   rectangular (no chevron shape). All segments are simple
   bordered boxes with the name + a » separator after each.
   Only the CURRENT (last) segment gets the chevron shape +
   softer grey colour to stand out as "you are here". The
   separator is the standard » (right-pointing double angle
   quotation mark) shown BETWEEN segments, not as part of
   the segment itself. */
/* Per user request 2026-06-20: border-bottom added back.
   Originally removed in Phase 115 because the 4-sided
   border looked too heavy; re-added with the simpler
   4-sided "border:" declaration (cleaner than 3 separate
   border-* properties). Collapsed to one line for
   readability - the block has no per-side variations, so
   the shorthand is the clearest expression. */
.breadcrumb-link { display: inline-flex; align-items: center; padding: 0.25rem 0.75rem; margin-right: 0.25rem; background: var(--bg-card); color: var(--fg-muted); text-decoration: none; border: 1px solid var(--border); border-radius: 3px; transition: background 0.12s, color 0.12s; }
.breadcrumb-link:hover {
  background: var(--bg-hover);
  color: var(--fg);
}
/* Per user request 2026-06-20: the » separator sits between
   segments. It's the standard HTML entity &raquo; (= » =
   right-pointing double angle quotation mark), shown with
   user-select: none so it can't be selected along with text.
   Same colour as the border-strong token so it visually
   connects to the segments but doesn't compete with the
   text. */
.breadcrumb-sep {
  color: var(--border-strong);
  user-select: none;
  font-size: 0.95rem;
  line-height: 1;
  margin-right: 0.25rem;
}
/* Per user request 2026-06-20: the CURRENT (last) segment
   gets the chevron shape and the softer colour scheme. It's
   the visual "you are here" indicator. --border-strong (medium
   grey) + --fg (readable text) in both light and dark mode. */
.breadcrumb-current {
  display: inline-flex;
  align-items: center;
  padding: 0.25rem 0.75rem;
  background: var(--border-strong);
  color: var(--fg);
  font-weight: 500;
  /* Chevron shape: pointed right edge (12px protrusion).
     Keeps the chevron on the CURRENT segment only, not all. */
  clip-path: polygon(0 0, calc(100% - 12px) 0, 100% 50%, calc(100% - 12px) 100%, 0 100%);
}
.breadcrumb-sep {
  /* Per user request 2026-06-20: chevron separator instead of
     a slash. The Unicode "›" character (single right-pointing
     angle quotation mark) reads as a chevron and is the
     standard "next level" symbol in file browsers. */
  color: var(--border-strong);
  user-select: none;
  font-size: 1rem;
  line-height: 1;
}
.breadcrumb-current {
  color: var(--fg);
  font-weight: 500;
}

/* Per user request 2026-06-20 (Phase 4): the file-type
   filter UI sits between the breadcrumb and the media
   section. The row is a single horizontal bar of "pills"
   (the All button + three dropdown triggers + the Apply
   button). Each dropdown opens to a vertical list of
   checkbox options (sub-type + count).

   Implementation: <details>/<summary> for show/hide (no JS,
   keyboard-accessible, mobile-friendly). The form uses
   standard browser submission (GET + checkboxes) so the
   filter is bookmarkable.

   Visual style: matches the existing sort-bar / breadcrumb
   — small text (0.85rem), muted color for inactive state,
   accent color for active. The Apply button is a primary
   CTA (slightly more prominent than the pills). */
.filter-form {
  /* Per user request 2026-06-20: padding now matches .sort-bar
     (0.75rem 0 0.75rem 0 — no horizontal padding). All three
     header rows (filter, breadcrumb, sort-bar) now have the
     same vertical padding and align to the page edge, so the
     left edge of the row aligns with the rest of the page
     content. */
  padding: 0.75rem 0 0.75rem 0;
}
.filter-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.4rem;
  font-size: 0.85rem;
}
.filter-label {
  color: var(--fg-faint);
  margin-right: 0.25rem;
}
.filter-pill {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  padding: 0.3rem 0.65rem;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--bg-card);
  color: var(--fg-muted);
  text-decoration: none;
  cursor: pointer;
  font-size: 0.85rem;
  transition: background 0.12s, border-color 0.12s, color 0.12s;
}
.filter-pill:hover {
  background: var(--bg-hover);
  border-color: var(--border-strong);
  color: var(--fg);
}
/* "All" and the dropdown triggers use a different visual
   treatment when active (currently selected or the dropdown
   is open). The summary of a <details> element can be styled
   with [open] to indicate the dropdown is open. */
.filter-pill-active,
.filter-dropdown[open] > .filter-pill,
.filter-pill:active {
  background: var(--bg-hover);
  border-color: var(--border-strong);
  color: var(--fg);
}

/* Per user request 2026-06-20: style the page-size <select>
   to match the filter-pill look. Same border, padding, font
   size, colors; same hover behaviour. The native caret is
   preserved by the browser (uses system UI). */
.page-size-select {
  font-family: inherit;
  font-size: 0.85rem;
  padding: 0.3rem 0.65rem;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--bg-card);
  color: var(--fg-muted);
  cursor: pointer;
  transition: background 0.12s, border-color 0.12s, color 0.12s;
  margin-top: -0.25rem;
  margin-left: 0.25rem;
}
.page-size-select:hover {
  background: var(--bg-hover);
  border-color: var(--border-strong);
  color: var(--fg);
}
.page-size-select:focus {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
}
.filter-count {
  color: var(--fg-faint);
  font-size: 0.8rem;
  font-variant-numeric: tabular-nums;
}
.filter-caret {
  color: var(--fg-faint);
  font-size: 0.7rem;
  line-height: 1;
  /* Make the caret point down when closed, up when open.
     <details> elements get a [open] attribute when expanded. */
}
.filter-dropdown[open] .filter-caret {
  transform: rotate(180deg);
}
/* <details> elements have a default disclosure triangle
   (the marker) that we don't want — we use the styled .filter-pill
   as the trigger instead. */
.filter-dropdown summary::-webkit-details-marker { display: none; }
.filter-dropdown summary { list-style: none; }
/* The dropdown body is the panel of checkbox options that
   appears below the trigger when the dropdown is open.
   Absolute positioning so it doesn't push the rest of the
   page down when opening. */
.filter-dropdown {
  position: relative;
}
.filter-dropdown-body {
  position: absolute;
  top: 100%;
  left: 0;
  z-index: 10;
  min-width: 12rem;
  margin-top: 0.25rem;
  padding: 0.5rem;
  background: var(--bg-card);
  border: 1px solid var(--border-strong);
  border-radius: 4px;
  box-shadow: 0 4px 8px rgba(0, 0, 0, 0.1);
  max-height: 24rem;
  overflow-y: auto;
}
.filter-option {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.3rem 0.5rem;
  border-radius: 3px;
  cursor: pointer;
  user-select: none;
}
.filter-option:hover {
  background: var(--bg-hover);
}
.filter-option input[type="checkbox"] {
  margin: 0;
  cursor: pointer;
}
.filter-option-name {
  flex: 1;
  font-family: monospace;
  font-size: 0.8rem;
}
.filter-option-count {
  color: var(--fg-faint);
  font-size: 0.8rem;
  font-variant-numeric: tabular-nums;
}
/* Per user request 2026-06-20: the Apply button uses the
   --active-bg / --active-fg / --active-border colour scheme,
   which is the OPPOSITE mode's page colour (light mode =
   dark with light text; dark mode = light with dark text).
   This matches the active sort button and active pagination
   button — the three "primary action" elements across the
   gallery share the same visual treatment, in both light
   and dark mode.

   The previous version used --accent (blue) which stood out
   but didn't match the rest of the action buttons. Now the
   Apply button looks like the other active buttons, which
   is consistent and works in both colour schemes. */
.filter-apply {
  display: inline-flex;
  align-items: center;
  padding: 0.3rem 0.85rem;
  border: 1px solid var(--active-border);
  border-radius: 4px;
  background: var(--active-bg);
  color: var(--active-fg);
  font-size: 0.85rem;
  font-weight: 500;
  cursor: pointer;
  transition: background 0.12s, color 0.12s, border-color 0.12s;
}
.filter-apply:hover {
  background: var(--bg-hover);
  color: var(--active-fg);
  border-color: var(--active-border);
}

/* Phase 118: search controls (Phase 118).
   Per user request 2026-06-20: add a search box + "Search all"
   button on the right of the filter row. The search box is a
   native <input type="search"> styled to match the filter
   pills (border, padding, font, background, hover behaviour).
   The button matches the .filter-apply look. The
   .search-controls wrapper is right-aligned inside the
   .filter-row via margin-left: auto. */
.search-controls {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  margin-left: auto;
  font-size: 0.85rem;
}
.search-input {
  font-family: inherit;
  font-size: 0.85rem;
  padding: 0.3rem 0.65rem;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--bg-card);
  color: var(--fg);
  width: 12rem;
  transition: background 0.12s, border-color 0.12s, color 0.12s;
}
.search-input::placeholder {
  color: var(--fg-faint);
}
.search-input:hover {
  background: var(--bg-hover);
  border-color: var(--border-strong);
}
.search-input:focus {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
  border-color: var(--accent);
}
.search-button {
  display: inline-flex;
  align-items: center;
  padding: 0.3rem 0.85rem;
  border: 1px solid var(--border);
  border-radius: 4px;
  background: var(--bg-card);
  color: var(--fg-muted);
  font-size: 0.85rem;
  font-weight: 500;
  cursor: pointer;
  font-family: inherit;
  transition: background 0.12s, border-color 0.12s, color 0.12s;
}
.search-button:hover {
  background: var(--bg-hover);
  border-color: var(--border-strong);
  color: var(--fg);
}

/* Phase 118: cards that don't match the current search get
   this class. visibility: collapse is the grid-aware collapse
   (entire rows that have no matches collapse too, so matches
   cluster at the top rather than scattering through empty
   grid cells). opacity: 0 + transition gives the fade. */
.card,
.files-table tbody tr {
  transition: opacity 0.2s;
}
.card.filtered-out,
.files-table tbody tr.filtered-out {
  visibility: collapse;
  opacity: 0;
  pointer-events: none;
  /* Defensive: tabindex/aria-hidden are toggled in JS too so
     screen readers and keyboard users skip them. */
}


/* Per Phase 85: the sort-by arrow (↑/↓ on the active sort
   button) inherits its color from the active button's text
   color (--active-fg, set by .sort-btn.active above). The
   arrow doesn't need its own color rule — it just inherits
   the active fg. This means the arrow is dark on a light
   button (light mode) or light on a dark button (dark mode),
   which is the correct contrast for each theme. */
.media-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: 1rem;
}
.card {
  display: flex;
  flex-direction: column;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 5px;
  overflow: hidden;
  text-decoration: none;
  color: inherit;
  transition: border-color 0.12s, transform 0.12s;
}
.card:hover { border-color: var(--accent); transform: translateY(-1px); }
.thumb {
  position: relative;
  width: 100%;
  aspect-ratio: 1 / 1;
  background: var(--bg-chip);
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
}
/* Per user request 2026-06-27: a subtle shimmer animation
   while the thumbnail image is loading. The shimmer is a
   diagonal sweep of slightly-brighter pixels that loops
   across the thumbnail. Stops automatically when the <img>
   finishes loading (JS removes the .loading class).

   The animation is GPU-friendly (transform-based), pauses
   on prefers-reduced-motion, and uses opacity to blend with
   the underlying --bg-chip so it works in both light and
   dark mode. */
.thumb.loading::before {
  content: "";
  position: absolute;
  inset: 0;
  background: linear-gradient(
    100deg,
    transparent 20%,
    rgba(255, 255, 255, 0.12) 50%,
    transparent 80%
  );
  transform: translateX(-100%);
  animation: thumb-shimmer 1.4s ease-in-out infinite;
  pointer-events: none;
  z-index: 0;
}
@keyframes thumb-shimmer {
  0%   { transform: translateX(-100%); }
  100% { transform: translateX(100%); }
}
@media (prefers-reduced-motion: reduce) {
  .thumb.loading::before { animation: none; }
}
.thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
/* Per user request 2026-06-27: the dimensions watermark
   appears at the bottom-LEFT of the IMAGE (inside .thumb
   div), always visible. Shows the W × H of the source image
   (or video). Styled like the open-btn (translucent
   background) but text instead of an icon, and ALWAYS
   visible (the open-btn is hover-only).

   Position: bottom-left of the IMAGE itself (not the
   card meta). The .thumb div is position: relative so the
   absolute positioning of this watermark is relative to
   the image bounds, not the card. */
.thumb-dimensions {
  position: absolute;
  bottom: 6px;
  left: 6px;
  padding: 2px 6px;
  background: rgba(0, 0, 0, 0.65);
  color: rgba(255, 255, 255, 0.95);
  border-radius: 3px;
  font-size: 0.65rem;
  font-weight: 600;
  letter-spacing: 0.02em;
  pointer-events: none;
  font-variant-numeric: tabular-nums;
  z-index: 1;
}
.open-btn {
  position: absolute;
  top: 6px;
  right: 6px;
  width: 28px;
  height: 28px;
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.85);
  /* Per user request 2026-06-18: the arrow inside the open-btn
     span should be #111111 (a fixed dark color), not the
     theme-aware --fg token. The --fg variable stays at its
     current value (#e5e5e5 in dark mode, #111111 in light mode)
     and continues to be used by other elements. The open-btn
     arrow is intentionally a different color than --fg
     because the button has a light translucent bg
     (rgba(255,255,255,0.85)) that stays light in both modes —
     a dark arrow on a light bg is always visible. */
  color: #111111;
  font-size: 0.95rem;
  line-height: 1;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0 0 1px 1px; /* optical centering for ↗ glyph */
  opacity: 0.5;
  transition: opacity 0.12s, background 0.12s, transform 0.12s;
  z-index: 1;
  font-family: inherit;
  user-select: none;
  /* Per user request 2026-06-18: dark border so the button
     stands out more. The button has a light translucent bg
     (rgba(255,255,255,0.85)), so a dark border is visible
     in BOTH light and dark modes (the bg stays light over
     any page bg). */
  border: 2px solid #000;
}
.card:hover .open-btn,
.open-btn:hover,
.open-btn:focus,
.open-btn:focus-visible {
  opacity: 1;
  background: rgba(255, 255, 255, 0.98);
  outline: none;
}
.open-btn:hover,
.open-btn:focus,
.open-btn:focus-visible {
  transform: scale(1.1);
  box-shadow: 0 2px 6px rgba(0, 0, 0, 0.15);
}
.thumb-video {
  background: linear-gradient(135deg, #1a1a26 0%, #2d2d40 100%);
  display: flex;
  align-items: center;
  justify-content: center;
}
.play-overlay {
  /* Per Phase 62: position: absolute so the play button sits
     on top of the video thumbnail image (when present) instead
     of competing with it in the flex layout. Without this,
     adding an <img> as a sibling would push the play overlay
     out of center. The parent .thumb has position: relative
     already, so this anchors to the thumb area. */
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  width: 64px;
  height: 64px;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.92);
  color: #1a1a26;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 1.6rem;
  padding-left: 0.35rem; /* optical centering for the ▶ glyph */
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.35);
  transition: transform 0.15s, background 0.15s;
}
.card.video:hover .play-overlay {
  /* Re-centre on hover (override the translate(-50%, -50%) so the
     scale transform stays centered). */
  transform: translate(-50%, -50%) scale(1.1);
  background: #fff;
}
.tile-name {
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--fg);
  padding: 0.5rem 0.6rem 0.15rem;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.tile-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.4rem;
  padding: 0.15rem 0.6rem 0.5rem;
  font-size: 0.7rem;
  color: var(--fg-faint);
  font-variant-numeric: tabular-nums;
}
.tile-meta-info {
  display: flex;
  flex-direction: column;
  gap: 0;
  min-width: 0;
  flex: 1 1 auto;
}
.tile-meta-info .date,
.tile-meta-info .size { line-height: 1.35; }
.filetype-chip {
  background: var(--bg-chip);
  color: var(--fg);
  padding: 0.1rem 0.4rem;
  border-radius: 3px;
  font-size: 0.65rem;
  font-weight: 700;
  letter-spacing: 0.05em;
  flex: 0 0 auto;
}
/* Per user request 2026-06-27: the EXIF pill appears below
   the filetype-chip on the card overlay, only when the image
   has EXIF metadata. The pill is just a label (not a link)
   to inform the visitor that more info is available in the
   lightbox. Same chip style as .filetype-chip but with a
   slightly different background to distinguish it. */
.tile-meta-chips {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 0.2rem;
  flex: 0 0 auto;
}
.exif-chip {
  background: var(--bg-active);
  color: var(--accent);
  padding: 0.1rem 0.4rem;
  border-radius: 3px;
  font-size: 0.65rem;
  font-weight: 700;
  letter-spacing: 0.05em;
  cursor: help;
}
.pagination {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  margin: 1.5rem 0; /* both top and bottom — applies to the bottom pagination (between media-section and the end of main) AND the new top pagination (between sort-bar and dirs-section) */
  font-size: 0.85rem;
}
.page-btn {
  display: inline-block;
  padding: 0.4rem 0.75rem;
  border: 1px solid var(--border);
  border-radius: 4px;
  color: var(--fg);
  text-decoration: none;
  background: var(--bg-card);
}
.page-btn:hover { background: var(--bg-hover); border-color: var(--border-strong); }
.page-btn.active {
  /* Per Phase 85: same color-contrast inversion as
     .sort-btn.active (above). The active page button uses
     --active-bg / --active-fg / --active-border which are the
     OPPOSITE mode's page colors. In light mode the active
     page is dark with light text; in dark mode it's light
     with dark text. */
  background: var(--active-bg);
  border-color: var(--active-border);
  color: var(--active-fg);
  cursor: default;
  pointer-events: none;
}
.page-btn.disabled {
  color: var(--fg-disabled);
  background: var(--bg-card);
  cursor: not-allowed;
  pointer-events: none;
}
.page-ellipsis {
  /* The "..." in the Google-style pagination. Same color as the
     page-btn text but no border or background — just a visual
     separator between the numbered buttons. */
  padding: 0.4rem 0.25rem;
  color: var(--fg-faint);
  user-select: none;
}
.page-info {
  padding: 0 0.75rem;
  color: var(--fg-muted);
}
.empty {
  padding: 2rem;
  text-align: center;
  color: var(--fg-faint);
}
@media (max-width: 600px) {
  body { padding: 1rem 0.5rem 3rem; }
  header, .dirs-section, .others-section, .media-section { padding-left: 1rem; padding-right: 1rem; }
  .media-grid { grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); }
  /* Per user request 2026-06-18: on small screens, stack the
     .header-top vertically so the meta line wraps onto a new
     line (instead of being squished by the flex parent).
     Without this, the meta line ("375 images ... 50 per page")
     gets compressed on phones and can overflow horizontally. */
  .header-top { flex-direction: column; }
  /* Theme toggle now sits ABOVE the title and meta line (was
     below in Phase 50; user wants the toggle at the top).
     Use CSS 'order' to reorder without changing the HTML
     source order (which matters for accessibility / reading
     order). Right-align the toggle in its own row. */
  .header-main { order: 2; }
  .theme-toggle { order: 1; align-self: flex-end; }
  /* Phase 118: on small screens, the search box moves ABOVE
     the filter controls. The .filter-row wraps naturally,
     so just push the search-controls to the top of the wrap
     and make it full-width. */
  .filter-row { flex-direction: column; align-items: stretch; }
  .search-controls {
    margin-left: 0;
    margin-bottom: 0.5rem;
    width: 100%;
  }
  .search-input { flex: 1 1 auto; width: auto; }
}

/* Site footer (Phase 56): "proudly served by caddy + synapticloop"
   credit at the bottom of the page. Subtle — muted color, small
   text, centered, with a top border to separate it from the rest
   of the page content. The two brand names are linked to their
   respective homepages (caddyserver.com, github.com/synapticloop).
   rel="noopener" is the standard security practice for
   target="_blank" links. */
.site-footer {
  text-align: center;
  padding: 1.5rem 1rem 2rem;
  font-size: 0.8rem;
  color: var(--fg-muted);
  /* Per user request 2026-06-19: removed the border-top. The
     footer now blends into the page content above without a
     visible separator. Padding-top (1.5rem) still provides
     visual breathing room. */
  margin-top: 1rem;
}
.site-footer a {
  color: var(--accent);
  text-decoration: none;
}
.site-footer a:hover {
  text-decoration: underline;
}

/* ---- Lightbox overlay (created by lightbox.js) ---- */
#gallery-lightbox {
  position: fixed;
  inset: 0;
  background: rgba(20, 22, 28, 0.96);
  display: none;
  align-items: center;
  justify-content: center;
  z-index: 9999;
  animation: lb-fade-in 0.12s ease-out;
}
#gallery-lightbox.open { display: flex; }
@keyframes lb-fade-in { from { opacity: 0; } to { opacity: 1; } }
#gallery-lightbox img,
#gallery-lightbox video {
  max-width: 95vw;
  max-height: 90vh;
  object-fit: contain;
  box-shadow: 0 0 60px rgba(0, 0, 0, 0.6);
  border-radius: 4px;
}
#gallery-lightbox .lb-btn {
  position: absolute;
  background: none;
  border: none;
  color: rgba(255, 255, 255, 0.85);
  font-size: 2.4rem;
  cursor: pointer;
  padding: 0.5rem 1rem;
  line-height: 1;
  transition: color 0.15s;
  font-family: inherit;
}
/* Per user request 2026-06-20: prev/next buttons fill the full
   window height and extend ~120px into the window width, with a
   subtle hover background. The hit area is large (full height
   makes them easy to click on touch devices; the ~120px width
   gives a clear target without obscuring the media).

   The hover color uses an alpha-blended fill so it works on top
   of any media background:
     - Dark mode (light overlay): rgba(255, 255, 255, 0.08) —
       a subtle "whiter" tint on the dark lightbox bg
     - Light mode (darker overlay): rgba(0, 0, 0, 0.06) — a
       subtle "darker" tint

   The lightbox itself is theme-independent (always dark), so
   the dark mode tint is the default. A :root[data-theme="light"]
   override would be needed for an actually-light lightbox, but
   since we don't have one, the dark tint is correct for both
   page themes.

   Both buttons have z-index: 1 so they sit ABOVE the media
   element (the media click handler is short-circuited for
   <video> in Phase 61; for <img> the buttons stopPropagation
   when their own click handler fires).

   The arrow icon is centered visually inside the wide hit area
   via flex. The hit area itself is invisible at rest (no bg)
   — only the hover reveals the target. */
#gallery-lightbox .lb-prev,
#gallery-lightbox .lb-next {
  top: 0;
  height: 100vh;
  width: 120px;
  display: flex;
  align-items: center;
  justify-content: center;
  /* Replace the previous "vertical center via translate" with
     this full-height layout. The arrow is flex-centered. */
  transform: none;
  padding: 0;
  background: transparent;
  transition: background 0.18s, color 0.15s;
}
#gallery-lightbox .lb-prev { left: 0; }
#gallery-lightbox .lb-next { right: 0; }
#gallery-lightbox .lb-prev:hover,
#gallery-lightbox .lb-next:hover {
  /* Dark mode hover (the default — lightbox is always dark). */
  background: rgba(255, 255, 255, 0.08);
}
:root[data-theme="light"] #gallery-lightbox .lb-prev:hover,
:root[data-theme="light"] #gallery-lightbox .lb-next:hover {
  /* Light mode hover: a darker tint. The lightbox itself is
     still dark (theme-independent per existing design), but the
     page-level theme is light, so we use a darker bg tint for
     contrast. */
  background: rgba(0, 0, 0, 0.06);
}
#gallery-lightbox .lb-close { top: 1rem; right: 1.5rem; }
/* Revert the previous .lb-prev / .lb-next transform/position rules
   (the more specific selectors above override them; keeping them
   would be a no-op but the comments help future readers understand
   the design intent). */
/* Lightbox controls container — wraps the "open in new tab"
   and "close" buttons in a rounded pill. Per user request
   2026-06-18: the two buttons should be aligned (same y,
   same size, in a flex container) inside a rounded box with
   a visible background. The pill sits at top-right of the
   lightbox, just above where lb-prev/next are on the sides. */
#gallery-lightbox .lb-controls {
  position: absolute;
  top: 1rem;
  right: 1.5rem;
  display: flex;
  gap: 0;
  /* Per user request 2026-06-19 (Phase 91: revert Phase 86
     + 88): restored the .lb-controls pill's background +
     border + border-radius + padding. Phases 86 and 88
     moved the pill styling onto each individual button so
     the rotated text labels could be inside the same grey
     rounded background. The user wants the text labels
     GONE, so we revert to the Phase 82 state: the pill
     has the background, each button is a transparent
     28x28 square inside it. */
  padding: 4px;
  background: rgba(255, 255, 255, 0.92);
  border: 2px solid #000;
  border-radius: 10px;
  z-index: 2;
}
/* Per user request 2026-06-19 (Phase 91: revert Phase 86
   + 88): the buttons are now simple 28x28 transparent
   squares inside the lb-controls pill. The pill itself
   has the background + border + border-radius; the
   buttons are transparent. This is the Phase 82 state
   (before the text labels were added). */
#gallery-lightbox .lb-controls .lb-btn {
  position: static;
  background: transparent;
  border: none;
  width: 28px;
  height: 28px;
  color: #1a1a26;
  font-size: 1.1rem;
  line-height: 1;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 0;
  border-radius: 6px;
  font-family: inherit;
  transition: background 0.12s;
}
/* Per user request 2026-06-19: the close × was visually
   smaller than the open ↗ (× is a thin cross at the
   baseline, ↗ has a bigger visual footprint). The user
   asked for a bigger close icon. We use a larger X glyph
   (✕ U+2715 MULTIPLICATION X) and bump its font-size so
   it visually balances with ↗. */
#gallery-lightbox .lb-controls .lb-close {
  font-size: 1.4rem;
  font-weight: 500;
}
#gallery-lightbox .lb-controls .lb-btn:hover {
  background: rgba(0, 0, 0, 0.08);
}
#gallery-lightbox .lb-caption {
  position: absolute;
  bottom: 1.5rem;
  left: 50%;
  transform: translateX(-50%);
  color: rgba(255, 255, 255, 0.7);
  font-size: 12px;
  letter-spacing: 0.06em;
  text-align: center;
  max-width: 90vw;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
#gallery-lightbox .lb-exif {
  /* Per user request 2026-06-27: the EXIF panel appears in
     the lightbox below the caption, only when the current
     image has EXIF metadata. Shown as a compact 2-column
     table (label + value) for readability. Hidden via the
     hidden HTML attribute when there is no EXIF. */
  position: absolute;
  bottom: 3.5rem;
  left: 50%;
  transform: translateX(-50%);
  background: rgba(0, 0, 0, 0.55);
  color: rgba(255, 255, 255, 0.9);
  padding: 0.6rem 0.9rem;
  border-radius: 6px;
  font-size: 0.8rem;
  line-height: 1.5;
  white-space: nowrap;
  backdrop-filter: blur(4px);
}
#gallery-lightbox .lb-exif-table {
  border-collapse: collapse;
}
#gallery-lightbox .lb-exif-table td {
  padding: 0;
  vertical-align: top;
}
#gallery-lightbox .lb-exif-label {
  color: rgba(255, 255, 255, 0.55);
  padding-right: 0.6rem !important;
  text-transform: uppercase;
  font-size: 0.65rem;
  letter-spacing: 0.05em;
  font-weight: 600;
}
#gallery-lightbox .lb-counter {
  position: absolute;
  top: 1.2rem;
  left: 1.5rem;
  color: rgba(255, 255, 255, 0.55);
  font-size: 11px;
  letter-spacing: 0.08em;
}
@media (max-width: 600px) {
  #gallery-lightbox .lb-btn { font-size: 1.8rem; padding: 0.25rem 0.5rem; }
  #gallery-lightbox .lb-close { top: 0.5rem; right: 0.5rem; }
}
</style>
</head>
<body>
<main>
  <header>
    <div class="header-top">
      <div class="header-main">
        <h1>{{.Title}}</h1>
        <div class="meta">
          {{/* Per user request 2026-06-19: show the total
             number of files at the start of the meta line, then
             the breakdown (images / videos / other files). The
             total is a quick "how many files are in this dir"
             answer; the breakdown gives the detail.

             Pluralization: "1 file" vs "N files" (English
             singular/plural). The split is via a small
             {{if eq}} check on the .TotalFiles value. */}}
          <span>{{.TotalFiles}} {{if eq .TotalFiles 1}}file{{else}}files{{end}}</span>
          <span>//</span>
          <span>{{.ImageCount}} images</span>
          {{if gt .TotalVideos 0}}<span>·</span><span>{{.TotalVideos}} videos</span>{{end}}
          {{if gt (len .OtherFiles) 0}}<span>·</span><span>{{len .OtherFiles}} other files</span>{{end}}
          <span>//</span>
          <span>({{.TotalAllFilesSize}} total)</span>
          <span>//</span>{{if or .Up (gt (len .Subdirs) 0)}} <span>{{if .Up}}{{len .Subdirs}} {{else}}{{len .Subdirs}}{{end}} directories</span>{{end}}
          <!-- Per user request 2026-06-20: a dropdown for page size.
     Shows the operator-configured list (defaults to
     30, 60, 120, "all"). "all" is a special token meaning
     "show all items in one page" - only included if the
     operator explicitly listed it. Each option is a link
     to the same URL with ?page_size=N appended (or removed
     for "all"). The current selection is shown as selected. -->
  <span>·</span>
  <span>Show</span>
  <form method="get" action="" class="page-size-form">
    {{- /* Preserve other URL params (sort, filter, breadcrumb) when submitting. */ -}}
    {{- /* Per user request 2026-06-27: EXCLUDE the "page" param -}}
    {{- /* so changing page size always resets to page 1 (the */ -}}
    {{- /* current page number might not exist in the new size). */ -}}
    {{- queryToHiddenInputsExclude $.Query "page" -}}
    <select name="page_size" class="page-size-select" onchange="this.form.submit()">
      {{$pageSizeStr := printf "%d" $.PageSize}}
      {{range .PageSizes}}
      <option value="{{.}}"{{if eq $pageSizeStr .}} selected{{end}}>{{if eq . "all"}}all{{else}}{{.}}{{end}}</option>
      {{end}}
    </select>
  </form>
  <span>Per page</span>{{if gt .TotalPages 1}}<span>·</span><span>Page {{.Page}} of {{.TotalPages}}</span>{{end}}
        </div>
      </div>
      <!-- Per user request 2026-06-18: removed the top-right
           sort order indicator (the "Sort: Name ↓" link). The sort
           UI still exists as the sort-bar below the header (with
           Name/Type/Modified/Size buttons), so the sort order is
           still visible there. The theme toggle moves to the top-
           right (where the sort indicator was). -->
      <div class="theme-toggle" role="radiogroup" aria-label="Theme">
        <button type="button" data-theme="auto" aria-pressed="false" aria-label="Auto (follow system preference)" title="Auto">⚙</button>
        <button type="button" data-theme="light" aria-pressed="false" aria-label="Light mode" title="Light">☀</button>
        <button type="button" data-theme="dark" aria-pressed="false" aria-label="Dark mode" title="Dark">🌙</button>
      </div>
    </div>
    <div class="sort-bar">
      {{$q := .Query}}
      <span class="sort-label">Sort by</span>
      <a class="sort-btn{{if eq .Sort.Field "name"}} active{{end}}" href="?{{queryString (sortURL $q "name" (sortOrder .Sort.Field "name" .Sort.Order))}}">Name<span class="arrow">{{if eq .Sort.Field "name"}}{{if eq .Sort.Order "asc"}} ↑{{else}} ↓{{end}}{{end}}</span></a>
      <a class="sort-btn{{if eq .Sort.Field "type"}} active{{end}}" href="?{{queryString (sortURL $q "type" (sortOrder .Sort.Field "type" .Sort.Order))}}">Type<span class="arrow">{{if eq .Sort.Field "type"}}{{if eq .Sort.Order "asc"}} ↑{{else}} ↓{{end}}{{end}}</span></a>
      <a class="sort-btn{{if eq .Sort.Field "mtime"}} active{{end}}" href="?{{queryString (sortURL $q "mtime" (sortOrder .Sort.Field "mtime" .Sort.Order))}}">Modified<span class="arrow">{{if eq .Sort.Field "mtime"}}{{if eq .Sort.Order "asc"}} ↑{{else}} ↓{{end}}{{end}}</span></a>
      <a class="sort-btn{{if eq .Sort.Field "size"}} active{{end}}" href="?{{queryString (sortURL $q "size" (sortOrder .Sort.Field "size" .Sort.Order))}}">Size<span class="arrow">{{if eq .Sort.Field "size"}}{{if eq .Sort.Order "asc"}} ↑{{else}} ↓{{end}}{{end}}</span></a>
    </div>
    {{/* Per user request 2026-06-20 (Phase 3): breadcrumb
       below the sort-bar, above the media section. Each
       segment is a link to that directory level except the
       last (current dir, plain text). The filter (?type=)
       is preserved across breadcrumb clicks so the user
       doesn't lose their filter state when navigating up. */}}
    {{if gt (len .Breadcrumb) 0}}
    <nav class="breadcrumb" aria-label="Directory path">
      {{range $i, $seg := .Breadcrumb}}
        {{if eq $i (lastIndex $.Breadcrumb)}}
          <span class="breadcrumb-current">{{$seg.Name}}</span>
        {{else}}
          <a class="breadcrumb-link" href="{{$seg.Href}}{{if $.IsTypeFilterActive}}?type={{$.TypeFilterQuery}}{{end}}">{{$seg.Name}}</a>
        {{end}}
      {{end}}
    </nav>
    {{end}}

        {{if or (gt .FilterImageOptions.Total 0) (gt .FilterVideoOptions.Total 0) (gt .FilterOtherOptions.Total 0)}}
    <form class="filter-form" method="get" action="">
      <div class="filter-row">
        <span class="filter-label">Type Filter</span>

        <a class="filter-pill filter-all{{if not .IsTypeFilterActive}} filter-pill-active{{end}}"
           href="{{if .Breadcrumb}}{{(index .Breadcrumb (lastIndex .Breadcrumb)).Href}}{{else}}./{{end}}">All</a>

        {{if gt .FilterImageOptions.Total 0}}
        <details class="filter-dropdown">
          <summary class="filter-pill">
            {{.FilterImageOptions.Label}}
            <span class="filter-count">({{.FilterImageOptions.Selected}}/{{.FilterImageOptions.Total}})</span>
            <span class="filter-caret" aria-hidden="true">▾</span>
          </summary>
          <div class="filter-dropdown-body">
            {{range .FilterImageOptions.Options}}
            <label class="filter-option">
              <input type="checkbox" name="ext" value="{{.Ext}}" {{if .Selected}}checked{{end}}>
              <span class="filter-option-name">{{.DisplayExt}}</span>
              <span class="filter-option-count">({{.Count}})</span>
            </label>
            {{end}}
          </div>
        </details>
        {{end}}

        {{if gt .FilterVideoOptions.Total 0}}
        <details class="filter-dropdown">
          <summary class="filter-pill">
            {{.FilterVideoOptions.Label}}
            <span class="filter-count">({{.FilterVideoOptions.Selected}}/{{.FilterVideoOptions.Total}})</span>
            <span class="filter-caret" aria-hidden="true">▾</span>
          </summary>
          <div class="filter-dropdown-body">
            {{range .FilterVideoOptions.Options}}
            <label class="filter-option">
              <input type="checkbox" name="ext" value="{{.Ext}}" {{if .Selected}}checked{{end}}>
              <span class="filter-option-name">{{.DisplayExt}}</span>
              <span class="filter-option-count">({{.Count}})</span>
            </label>
            {{end}}
          </div>
        </details>
        {{end}}

        {{if gt .FilterOtherOptions.Total 0}}
        <details class="filter-dropdown">
          <summary class="filter-pill">
            {{.FilterOtherOptions.Label}}
            <span class="filter-count">({{.FilterOtherOptions.Selected}}/{{.FilterOtherOptions.Total}})</span>
            <span class="filter-caret" aria-hidden="true">▾</span>
          </summary>
          <div class="filter-dropdown-body">
            {{range .FilterOtherOptions.Options}}
            <label class="filter-option">
              <input type="checkbox" name="ext" value="{{.Ext}}" {{if .Selected}}checked{{end}}>
              <span class="filter-option-name">{{.DisplayExt}}</span>
              <span class="filter-option-count">({{.Count}})</span>
            </label>
            {{end}}
          </div>
        </details>
        {{end}}

        <button type="submit" class="filter-apply">Apply</button>
        {{- /* Search box + button on the right of the filter row.
            Client-side: as the user types, JS hides non-matching
            cards (see inline JS at the bottom of the template).
            Server-side: "Search all" submits the form with ?q=foo
            for a full-directory search. */ -}}
        <div class="search-controls">
          <input type="search" name="q" class="search-input"
            placeholder="Search filenames…"
            value="{{.SearchQuery}}"
            autocomplete="off"
            aria-label="Search filenames in this directory">
          <button type="submit" class="search-button">Search all</button>
        </div>
      </div>
    </form>
    {{end}}
    </header>

    {{if gt .TotalPages 1}}
    <!-- Per user request 2026-06-27: pagination links now
       preserve the full query (sort, order, type filter,
       search query, page_size) — only page is replaced. The
       old links only had sort+order+page, which lost the
       type filter and search query when navigating pages.
       The pattern is: build a query with page=N (using
       queryWith), render it with queryString. The leading
       "?" is always present (empty if the only param was
       the page we're replacing), so the URL is always
       a valid relative URL. -->
    <nav class="pagination">
    {{if .HasPrev}}
      <a class="page-btn" href="?{{queryString (queryForPage .Query .Sort (.Page | minus1))}}">← Prev</a>
    {{else}}
      <span class="page-btn disabled">← Prev</span>
    {{end}}
    {{range .PageNumbers}}
    {{if eq . 0}}
      <span class="page-ellipsis">…</span>
    {{else if eq . $.Page}}
      <span class="page-btn active">{{.}}</span>
    {{else}}
      <a class="page-btn" href="?{{queryString (queryForPage $.Query $.Sort .)}}">{{.}}</a>
    {{end}}
    {{end}}
    {{if .HasNext}}
      <a class="page-btn" href="?{{queryString (queryForPage .Query .Sort (.Page | plus1))}}">Next →</a>
    {{else}}
      <span class="page-btn disabled">Next →</span>
    {{end}}
    </nav>
    {{end}}

    {{if or .Up (gt (len .Subdirs) 0)}}
    <section class="dirs-section" data-section="dirs">
    <h2 class="section-heading">
      <span>Directories ({{len .Subdirs}})</span>
      <span class="heading-divider" aria-hidden="true"></span>
      <button type="button" class="section-toggle" data-toggle="dirs" aria-expanded="true" aria-controls="dirs-body" title="Show/hide directories">−</button>
    </h2>
    <div class="section-body" id="dirs-body">
    <!-- Per user request 2026-06-19: directory chips replaced
         with a full-width table. The Name column is the link
         (clicking navigates into the directory); Type shows
         "DIR"; Date shows the directory's mtime. Size is
         intentionally omitted for directories (meaningless
         — recursive size would require a separate scan).

         Per user request 2026-06-19: the Up row is now a
         separate <table class="up-row-table"> (no <thead>, no
         row-spacer) above the subdirs table. This avoids the
         up-spacer row (which used to highlight on hover because
         it inherited the tr:hover rule). The visual separation
         comes from the table's own bottom border + the top of
         the next table, NOT from an empty row. Each table is
         self-contained.

         Per user request 2026-06-19: subdirs table has its own
         <thead> with Name/Type/Modified headers (the user said
         "no need to repeat headers" -- that means: no separate
         header row above the up-row, but the subdirs table
         itself can have its own column headers since it's a
         different table). -->
    {{if .Up}}
    <table class="up-row-table">
      <tbody>
        <tr>
          <td colspan="2"><a class="table-link" href="{{.Up.Href}}"><span class="chip-icon">↑</span> <span class="chip-icon">📁</span> Up (../{{.Up.ParentDir}})</a></td>
        </tr>
      </tbody>
    </table>
    {{end}}
    {{if .Subdirs}}
    <table class="files-table dirs-table" id="dirs-table">
      <thead>
        <tr>
          <!-- Per user request 2026-06-27: each <th> is
               sortable via JS click. The data-sort-key
               attribute tells the JS handler what the column
               key is; data-default-order is the initial
               direction (asc or desc) when the user first
               clicks that column. The <span class="sort-indicator">
               inside the <th> shows ▲/▼ for the active sort.
               The default sort for the dirs table is
               "name asc" (matches the server-side order). -->
          <th class="col-name sortable" data-sort-key="name" data-default-order="asc"><span>Name</span><span class="sort-indicator"></span></th>
          <th class="col-count sortable" data-sort-key="items" data-default-order="desc"><span>#&nbsp;Items</span><span class="sort-indicator"></span></th>
          <th class="col-count sortable" data-sort-key="dirs" data-default-order="desc"><span>#&nbsp;Sub-Dirs</span><span class="sort-indicator"></span></th>
          <th class="col-size sortable" data-sort-key="size" data-default-order="desc"><span>Size</span><span class="sort-indicator"></span></th>
          <th class="col-date sortable" data-sort-key="date" data-default-order="desc"><span>Modified</span><span class="sort-indicator"></span></th>
        </tr>
      </thead>
      <tbody>
        {{range .Subdirs}}
        <tr data-name="{{.Name}}" data-items="{{.CountItems}}" data-dirs="{{.CountDirs}}" data-size="{{.Size}}" data-date="{{.ModTime}}">
          <td class="col-name"><a class="table-link" href="{{.Href}}"><span class="chip-icon">📁</span>{{.Name}}/</a></td>
          <td class="col-count"><a class="table-link cell-link" href="{{.Href}}" tabindex="-1" aria-hidden="true">{{.CountItems}}</a></td>
          <td class="col-count"><a class="table-link cell-link" href="{{.Href}}" tabindex="-1" aria-hidden="true">{{.CountDirs}}</a></td>
          <td class="col-size"><a class="table-link cell-link" href="{{.Href}}" tabindex="-1" aria-hidden="true">{{.Size}}</a></td>
          <td class="col-date"><a class="table-link cell-link" href="{{.Href}}" tabindex="-1" aria-hidden="true">{{.Date}}</a></td>
        </tr>
        {{end}}
      </tbody>
    </table>
    {{end}}
    </div>
  </section>
  {{end}}

  {{if .OtherFiles}}
  <section class="others-section" data-section="others">
    <h2 class="section-heading">
      <span>Other files ({{len .OtherFiles}})</span>
      <span class="heading-divider" aria-hidden="true"></span>
      <button type="button" class="section-toggle" data-toggle="others" aria-expanded="true" aria-controls="others-body" title="Show/hide other files">−</button>
    </h2>
    <div class="section-body" id="others-body">
    <!-- Per user request 2026-06-20: same full-width table format
         as the directories table. Adds a Size column (directories
         omitted Size because it's not meaningful for folders). -->
    <table class="files-table others-table" id="others-table">
      <thead>
        <tr>
          <!-- Per user request 2026-06-27: each <th> is
               sortable. data-sort-key tells the JS handler
               what column key this is. data-default-order is
               the initial direction. The default for the
               others table is "date desc" (matches the
               server-side mtime-desc default). -->
          <th class="col-name sortable" data-sort-key="name" data-default-order="asc"><span>Name</span><span class="sort-indicator"></span></th>
          <th class="col-type sortable" data-sort-key="type" data-default-order="asc"><span>Type</span><span class="sort-indicator"></span></th>
          <th class="col-size sortable" data-sort-key="size" data-default-order="desc"><span>Size</span><span class="sort-indicator"></span></th>
          <th class="col-date sortable" data-sort-key="date" data-default-order="desc"><span>Modified</span><span class="sort-indicator"></span></th>
        </tr>
      </thead>
      <tbody>
        {{range .OtherFiles}}
        <tr data-filename="{{.Name}}" data-name="{{.Name}}" data-type="{{.Type}}" data-size="{{.Size}}" data-date="{{.ModTime}}">
          <td class="col-name"><a class="table-link" href="{{.Href}}"><span class="chip-icon">📄</span>{{.Name}}</a></td>
          <td class="col-type"><a class="table-link cell-link" href="{{.Href}}" tabindex="-1" aria-hidden="true">{{.Type}}</a></td>
          <td class="col-size"><a class="table-link cell-link" href="{{.Href}}" tabindex="-1" aria-hidden="true">{{.Size}}</a></td>
          <td class="col-date"><a class="table-link cell-link" href="{{.Href}}" tabindex="-1" aria-hidden="true">{{.Date}}</a></td>
        </tr>
        {{end}}
      </tbody>
    </table>
    </div>
  </section>
  {{end}}

  <section class="media-section" data-section="media">
    <h2 class="section-heading">
      <span>Media ({{.TotalImages}}{{if and (gt .ImageStart 0) (gt .ImageEnd 0)}} - Showing {{.ImageStart}}-{{.ImageEnd}}{{end}})</span>
      <span class="heading-divider" aria-hidden="true"></span>
      <button type="button" class="section-toggle" data-toggle="media" aria-expanded="true" aria-controls="media-body" title="Show/hide media">−</button>
    </h2>
    <div class="section-body" id="media-body">
    <div class="media-grid">
      {{range .Images}}
      <a class="card{{if .IsVideo}} video{{end}}" data-filename="{{.Name}}" href="{{.Href}}"{{if and .Exif .Exif.HasAny}} data-exif-camera-make="{{.Exif.CameraMake}}" data-exif-camera-model="{{.Exif.CameraModel}}" data-exif-lens="{{.Exif.LensModel}}" data-exif-date="{{.Exif.DateTaken}}" data-exif-shutter="{{.Exif.ExposureTime}}" data-exif-aperture="{{.Exif.Aperture}}" data-exif-iso="{{.Exif.ISO}}" data-exif-focal="{{.Exif.FocalLength}}"{{end}}>
        <div class="thumb{{if .IsVideo}} thumb-video{{end}}">
          {{if .IsVideo}}
          {{if .ThumbURL}}
          <!-- Per Phase 62: video has a real thumbnail (extracted
               from the first frame by ffmpeg on the server). Show
               the <img> with the play overlay on top. -->
          <img class="thumb-img" loading="lazy" src="{{.ThumbURL}}" alt="">
          {{end}}
          <div class="play-overlay">▶</div>
          {{else}}
          <img loading="lazy" src="{{.ThumbURL}}" alt="{{.Name}}">
          {{end}}
          {{if .Dimensions}}<span class="thumb-dimensions">{{.Dimensions}}</span>{{end}}
          <span class="open-btn" data-open-url="{{.Href}}" role="button" tabindex="0" title="Open in new tab" aria-label="Open in new tab">↗</span>
        </div>
        <div class="tile-name">{{.Name}}</div>
        <div class="tile-meta">
          <div class="tile-meta-info">
            <span class="date">{{.Date}}</span>
            <span class="size">{{.Size}}</span>
          </div>
          <div class="tile-meta-chips">
            <span class="filetype-chip">{{.Type}}</span>
            {{if and .Exif .Exif.HasAny}}<span class="exif-chip" title="This image has EXIF metadata — viewable in the lightbox">EXIF</span>{{end}}
          </div>
        </div>
      </a>
      {{end}}
    </div>

    {{if gt .TotalPages 1}}
    <nav class="pagination">
      {{if .HasPrev}}
        <a class="page-btn" href="?{{queryString (queryForPage .Query .Sort (.Page | minus1))}}">← Prev</a>
      {{else}}
        <span class="page-btn disabled">← Prev</span>
      {{end}}
      {{range .PageNumbers}}
      {{if eq . 0}}
        <span class="page-ellipsis">…</span>
      {{else if eq . $.Page}}
        <span class="page-btn active">{{.}}</span>
      {{else}}
        <a class="page-btn" href="?{{queryString (queryForPage $.Query $.Sort .)}}">{{.}}</a>
      {{end}}
      {{end}}
      {{if .HasNext}}
        <a class="page-btn" href="?{{queryString (queryForPage .Query .Sort (.Page | plus1))}}">Next →</a>
      {{else}}
        <span class="page-btn disabled">Next →</span>
      {{end}}
    </nav>
    {{end}}
    </div>
    {{if eq (len .Images) 0}}
    <p class="empty">No images match the current filter.</p>
    {{end}}
  </section>
</main>
<footer class="site-footer">
  proudly served by <a href="https://caddyserver.com" rel="noopener" target="_blank">caddy</a> + <a href="https://github.com/synapticloop/caddy_media_gallery" rel="noopener" target="_blank">synapticloop // media gallery</a>
</footer>
<script>

/* Per user request 2026-06-27: shimmer animation while
   thumbnails are loading. Adds .loading class to every
   .thumb on page load; removes it when the <img> fires
   load (or error — either way the placeholder isn't
   needed). The CSS animates a diagonal sweep across the
   thumb. Stops automatically when the image loads.

   Edge case: if the image is already cached (instant load
   from the browser cache), the load event might fire
   before this script runs. In that case, the .loading class
   is briefly applied but removed within a frame. Not
   visible to the user.

   For images below the fold (lazy-loaded), the <img> won't
   start loading until it's near the viewport, so the
   .loading class stays applied until the user scrolls down.
   This is intentional — the shimmer only runs while the
   user can see the image, not for hidden thumbs. */
(function() {
  var thumbs = document.querySelectorAll('.thumb img');
  for (var i = 0; i < thumbs.length; i++) {
    (function(img) {
      var thumb = img.closest('.thumb');
      if (!thumb) return;
      // If the image is already complete (cached or
      // inline data URL), skip the loading state.
      if (img.complete && img.naturalWidth > 0) return;
      thumb.classList.add('loading');
      var done = function() {
        thumb.classList.remove('loading');
      };
      img.addEventListener('load', done, { once: true });
      img.addEventListener('error', done, { once: true });
    })(thumbs[i]);
  }
})();

/* Phase 118: client-side filename search filter.
   As the user types in the search input, items that don't match
   the query get the .filtered-out class. Matches the same
   word-boundary rule as the server (Go) side:
     - Lowercase both sides.
     - Split the filename on _, -, and space (each segment is a
       "word"). Split the query on whitespace.
     - A match occurs when any filename word starts with any
       query word.
   For example, q="cat" matches: cat.jpg, cat-photo.jpg,
   my_cat.webp, category-icon.svg (NOT scatter.png).
   Empty query = no filter. */
(function() {
  var input = document.querySelector('.search-input');
  if (input) {
    var debounceTimer;
    function applyFilter() {
      var raw = (input.value || '').toLowerCase().trim();
      var query = raw.length ? raw.split(/\s+/) : [];
      function matches(filename) {
        if (query.length === 0) return true;
        var name = filename.toLowerCase();
        var words = name.split(/[_\-\s]+/);
        for (var i = 0; i < words.length; i++) {
          for (var j = 0; j < query.length; j++) {
            if (words[i].indexOf(query[j]) === 0) return true;
          }
        }
        return false;
      }
      var cards = document.querySelectorAll('.media-grid .card[data-filename]');
      for (var i = 0; i < cards.length; i++) {
        var c = cards[i];
        var fn = c.getAttribute('data-filename') || '';
        if (matches(fn)) {
          c.classList.remove('filtered-out');
          c.removeAttribute('aria-hidden');
          c.setAttribute('tabindex', c.getAttribute('data-orig-tabindex') || '0');
        } else {
          c.classList.add('filtered-out');
          c.setAttribute('aria-hidden', 'true');
          c.setAttribute('tabindex', '-1');
        }
      }
      var rows = document.querySelectorAll('.files-table tbody tr[data-filename]');
      for (var i = 0; i < rows.length; i++) {
        var r = rows[i];
        var fn = r.getAttribute('data-filename') || '';
        if (matches(fn)) {
          r.classList.remove('filtered-out');
        } else {
          r.classList.add('filtered-out');
        }
      }
    }
    input.addEventListener('input', function() {
      clearTimeout(debounceTimer);
      debounceTimer = setTimeout(applyFilter, 100);
    });
    /* If the page was server-rendered with a ?q= value (because
       the visitor used "Search all" to do a full-directory search),
       apply that filter on page load too. */
    if (input.value && input.value.length) {
      applyFilter();
    }
  }
})();

/* Phase 163: click-to-sort table column headers.
   Per user request 2026-06-27: dirs and others tables have
   clickable column headers that sort the rows client-side.
   No page reload — JS reorders the <tbody> rows in place.

   State (in priority order):
     1. URL query string (?dirs_sort=name&dirs_order=asc) —
        the canonical state. New tabs / shared links pick
        this up automatically.
     2. localStorage (gallery-dirs-sort, gallery-dirs-order,
        gallery-others-sort, gallery-others-order) — the
        visitor's preferred sort. Persists across page
        reloads and tab closes.
     3. Default: dirs = name asc, others = date desc
        (matches the existing server-side order).

   On click:
     - Same column → toggle direction
     - Different column → use that column's data-default-order
     - Sort client-side, save to localStorage, update URL
       via history.replaceState (no reload, no history
       pollution). */
(function() {
  function getParam(name) {
    var m = window.location.search.match(new RegExp('[?&]' + name + '=([^&]*)'));
    return m ? decodeURIComponent(m[1]) : null;
  }
  function setParam(name, value) {
    var url = new URL(window.location.href);
    if (value == null) {
      url.searchParams.delete(name);
    } else {
      url.searchParams.set(name, value);
    }
    history.replaceState(null, '', url.toString());
  }

  function setupTable(tableId, paramPrefix, defaultSort, defaultOrder) {
    var table = document.getElementById(tableId);
    if (!table) return;
    var thead = table.querySelector('thead');
    var tbody = table.querySelector('tbody');
    if (!thead || !tbody) return;

    // Determine initial sort: URL > localStorage > default
    var initSort = getParam(paramPrefix + '_sort') ||
                   localStorage.getItem('gallery-' + paramPrefix + '-sort') ||
                   defaultSort;
    var initOrder = getParam(paramPrefix + '_order') ||
                    localStorage.getItem('gallery-' + paramPrefix + '-order') ||
                    defaultOrder;
    applySort(initSort, initOrder, false);

    // Wire up click handlers
    var ths = thead.querySelectorAll('th.sortable');
    for (var i = 0; i < ths.length; i++) {
      ths[i].addEventListener('click', (function(th) {
        return function() {
          var key = th.getAttribute('data-sort-key');
          var currentSort = localStorage.getItem('gallery-' + paramPrefix + '-sort') || defaultSort;
          var currentOrder = localStorage.getItem('gallery-' + paramPrefix + '-order') || defaultOrder;
          var newOrder;
          if (currentSort === key) {
            // Same column: toggle direction
            newOrder = currentOrder === 'asc' ? 'desc' : 'asc';
          } else {
            // Different column: use the column's default direction
            newOrder = th.getAttribute('data-default-order') || 'asc';
          }
          applySort(key, newOrder, true);
        };
      })(ths[i]));
    }

    function applySort(key, order, save) {
      // Find the th for this key
      var th = thead.querySelector('th[data-sort-key="' + key + '"]');
      if (!th) return;

      // Update indicators on all ths
      var allThs = thead.querySelectorAll('th.sortable');
      for (var i = 0; i < allThs.length; i++) {
        var ind = allThs[i].querySelector('.sort-indicator');
        if (ind) ind.textContent = '';
        allThs[i].classList.remove('sort-active');
      }
      var ind = th.querySelector('.sort-indicator');
      if (ind) ind.textContent = order === 'asc' ? '\u25B2' : '\u25BC';
      th.classList.add('sort-active');

      // Get rows and sort
      var rows = Array.prototype.slice.call(tbody.querySelectorAll('tr'));
      rows.sort(function(a, b) {
        var av = a.getAttribute('data-' + key) || '';
        var bv = b.getAttribute('data-' + key) || '';
        // Numeric vs string: detect by trying parseInt
        var an = parseFloat(av);
        var bn = parseFloat(bv);
        if (!isNaN(an) && !isNaN(bn) && av !== '' && bv !== '' && /^-?\d/.test(av) && /^-?\d/.test(bv)) {
          return order === 'asc' ? an - bn : bn - an;
        }
        // String compare
        if (av < bv) return order === 'asc' ? -1 : 1;
        if (av > bv) return order === 'asc' ? 1 : -1;
        return 0;
      });
      for (var i = 0; i < rows.length; i++) {
        tbody.appendChild(rows[i]);
      }

      if (save) {
        localStorage.setItem('gallery-' + paramPrefix + '-sort', key);
        localStorage.setItem('gallery-' + paramPrefix + '-order', order);
        setParam(paramPrefix + '_sort', key);
        setParam(paramPrefix + '_order', order);
      }
    }
  }

  setupTable('dirs-table', 'dirs', 'name', 'asc');
  setupTable('others-table', 'others', 'date', 'desc');
})();

(function() {
  var overlay = document.createElement('div');
  overlay.id = 'gallery-lightbox';
  overlay.innerHTML =
    '<div class="lb-controls">' +
      '<button class="lb-btn lb-open" aria-label="Open in new tab" title="Open in new tab">↗</button>' +
      '<button class="lb-btn lb-close" aria-label="Close" title="Close">✕</button>' +
    '</div>' +
    '<button class="lb-btn lb-prev" aria-label="Previous">‹</button>' +
    '<button class="lb-btn lb-next" aria-label="Next">›</button>' +
    '<span class="lb-counter"></span>' +
    '<span class="lb-caption"></span>' +
    '<div class="lb-exif" hidden>' +
      '<table class="lb-exif-table">' +
        '<tr><td class="lb-exif-label">Camera</td><td class="lb-exif-val" data-exif="camera"></td></tr>' +
        '<tr><td class="lb-exif-label">Lens</td><td class="lb-exif-val" data-exif="lens"></td></tr>' +
        '<tr><td class="lb-exif-label">Date</td><td class="lb-exif-val" data-exif="date"></td></tr>' +
        '<tr><td class="lb-exif-label">Exposure</td><td class="lb-exif-val" data-exif="exposure"></td></tr>' +
      '</table>' +
    '</div>';
  document.body.appendChild(overlay);

  var media = overlay.appendChild(document.createElement('div'));
  media.style.cssText = 'display:flex;align-items:center;justify-content:center;';
  var currentEl = null;
  var counter = overlay.querySelector('.lb-counter');
  var caption = overlay.querySelector('.lb-caption');

  // Media cards: image cards have an <img> child; video cards
  // have the .video class (with a <div class="thumb-video">
  // child instead of an <img>). Both open in the unified
  // lightbox. Other file types (no <img> and not .video) keep
  // their default link behavior.
  var cards = Array.prototype.slice.call(
    document.querySelectorAll('.card')
  ).filter(function(c) {
    return c.querySelector('img') || c.classList.contains('video');
  });
  var idx = 0;

  function clear() {
    // Pause the current element if it's a video (removing the
    // element from the DOM also stops playback, but explicit
    // pause is cleaner and matches the close-on-cleanup intent).
    if (currentEl) {
      if (typeof currentEl.pause === 'function') currentEl.pause();
      currentEl.remove();
      currentEl = null;
    }
  }

  function show(i) {
    if (cards.length === 0) return;
    idx = ((i % cards.length) + cards.length) % cards.length;
    var c = cards[idx];
    var href = c.getAttribute('href') || '';
    var name = (c.querySelector('.tile-name') || {}).textContent || '';
    var isVideo = c.classList.contains('video');
    clear();
    if (isVideo) {
      // Per user request 2026-06-19: show the video thumbnail
      // (extracted from the first frame by ffmpeg, served as a
      // WebP by the server) as the <video poster>. The browser
      // shows the poster image until the user clicks play, then
      // swaps to the first video frame and starts playback.
      //
      // The poster URL is the same as the <img class="thumb-img">
      // src that Phase 62 added to the tile card. We extract it
      // from the DOM rather than duplicating it in a data-*
      // attribute.
      var thumbImg = c.querySelector('img.thumb-img');
      var thumbSrc = thumbImg ? thumbImg.getAttribute('src') : '';
      var v = document.createElement('video');
      v.src = href;
      v.controls = true;
      v.preload = 'metadata';
      v.playsInline = true;
      v.alt = name;
      // Only set poster if a thumbnail URL is available. If ffmpeg
      // is missing or no_video_thumbs is set, the <img> won't be
      // in the card and we just skip poster (browser shows black
      // frame, same as before Phase 62).
      if (thumbSrc) v.poster = thumbSrc;
      currentEl = v;
    } else {
      var img = document.createElement('img');
      img.src = href;
      img.alt = name;
      currentEl = img;
    }
    media.appendChild(currentEl);
    counter.textContent = (idx + 1) + ' / ' + cards.length;
    caption.textContent = name;
    // Per user request 2026-06-27: show the EXIF metadata
    // in the lightbox. Read the data-exif-* attributes from
    // the card; if the card has none, hide the EXIF panel.
    var exifPanel = overlay.querySelector('.lb-exif');
    var cameraMake = c.getAttribute('data-exif-camera-make') || '';
    var cameraModel = c.getAttribute('data-exif-camera-model') || '';
    var lens = c.getAttribute('data-exif-lens') || '';
    var dateTaken = c.getAttribute('data-exif-date') || '';
    var shutter = c.getAttribute('data-exif-shutter') || '';
    var aperture = c.getAttribute('data-exif-aperture') || '';
    var iso = c.getAttribute('data-exif-iso') || '';
    var focal = c.getAttribute('data-exif-focal') || '';
    if (cameraMake || cameraModel || lens || dateTaken || shutter || aperture || iso || focal) {
      // Camera = "Make Model" (or just Make, or just Model)
      var camera = (cameraMake + (cameraMake && cameraModel ? ' ' : '') + cameraModel).trim();
      // Exposure = "Shutter · Aperture · ISO · Focal"
      var exposureParts = [shutter, aperture, iso, focal].filter(function(s) { return s; });
      var exposure = exposureParts.join(' · ');
      exifPanel.querySelector('[data-exif="camera"]').textContent = camera || '—';
      exifPanel.querySelector('[data-exif="lens"]').textContent = lens || '—';
      exifPanel.querySelector('[data-exif="date"]').textContent = dateTaken || '—';
      exifPanel.querySelector('[data-exif="exposure"]').textContent = exposure || '—';
      exifPanel.hidden = false;
    } else {
      exifPanel.hidden = true;
    }
    overlay.classList.add('open');
  }

  function close() {
    overlay.classList.remove('open');
    clear();
  }

  cards.forEach(function(c, i) {
    c.addEventListener('click', function(e) {
      // The open-btn (and its descendants) opens the file in a new
      // tab instead of the lightbox. Its own click handler calls
      // stopPropagation, but be defensive in case it doesn't.
      if (e.target.closest && e.target.closest('.open-btn')) return;
      e.preventDefault();
      show(i);
    });
  });

  // "Open in new tab" button on each tile. Clicking it (or pressing
  // Enter/Space when focused) opens the file URL in a new tab
  // instead of the lightbox. We stop propagation so the card's own
  // click handler (above) doesn't ALSO try to open the lightbox.
  document.querySelectorAll('.open-btn').forEach(function(btn) {
    var openUrl = function() {
      var url = btn.getAttribute('data-open-url');
      if (url) window.open(url, '_blank');
    };
    btn.addEventListener('click', function(e) {
      e.preventDefault();
      e.stopPropagation();
      openUrl();
    });
    btn.addEventListener('keydown', function(e) {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        e.stopPropagation();
        openUrl();
      }
    });
  });

  overlay.addEventListener('click', function(e) {
    if (e.target === overlay) close();
  });
  overlay.querySelector('.lb-close').addEventListener('click', close);
  // Per Phase 65: the prev/next hit areas are now 100vh tall x
  // 120px wide. They sit ON TOP of the media (z-index: 1). When
  // the user clicks inside the prev/next area, the button's click
  // fires (which advances/retreats by 1) AND the event bubbles to
  // media, which would ALSO advance (its click handler runs
  // show(idx+1) for <img>). Without stopPropagation here, clicking
  // "Next" would advance by 2 instead of 1.
  overlay.querySelector('.lb-prev').addEventListener('click', function(e) {
    e.stopPropagation();
    show(idx - 1);
  });
  overlay.querySelector('.lb-next').addEventListener('click', function(e) {
    e.stopPropagation();
    show(idx + 1);
  });
  // Open-in-new-tab: opens the current image/video URL in a new
  // tab. Uses cards[idx].href (the same href that show() set on
  // currentEl). window.open with _blank is the standard way; the
  // noopener/noreferrer flags prevent the new tab from accessing
  // window.opener (security best practice).
  overlay.querySelector('.lb-open').addEventListener('click', function() {
    if (cards.length === 0) return;
    var href = cards[idx].getAttribute('href') || '';
    if (href) window.open(href, '_blank', 'noopener,noreferrer');
  });
  // Click on the media area advances to the next item. EXCEPT
  // when the current media is a <video>: on mobile, tapping the
  // video's native play button fires a click event on the
  // <video> element, and the default click handler advances to
  // the next file BEFORE the video can play (a really bad UX --
  // the user taps play, expects the video to start, and instead
  // sees the next image). Detect this case and bail out so the
  // browser's native click handling (play / pause) takes over.
  // The user can still navigate via the prev/next buttons or
  // the arrow keys.
  media.addEventListener('click', function(e) {
    if (currentEl && currentEl.tagName === 'VIDEO') return;
    e.stopPropagation();
    show(idx + 1);
  });
  document.addEventListener('keydown', function(e) {
    if (!overlay.classList.contains('open')) return;
    if (e.key === 'Escape') close();
    else if (e.key === 'ArrowLeft') show(idx - 1);
    else if (e.key === 'ArrowRight') show(idx + 1);
  });

    /* === SECTION TOGGLE (Phase 71, extended in Phase 74) =========
       The directories + other-files sections each have a toggle
       behavior: clicking the section heading (the whole width)
       collapses the body (display: none) and updates the
       button's text + aria. The state is persisted in localStorage
       so the visitor's choice survives navigations and refreshes.

       Per user request 2026-06-19: the WHOLE WIDTH of the section
       heading is now clickable, not just the small − / + button.
       The button is still rendered (and is still keyboard-tabbable
       + screen-reader-announceable) as a visual affordance +
       a11y target, but the user can click anywhere on the
       heading row to toggle.

       Why localStorage (not URL query / not page state):
         - URL query would be bookmarkable, but the toggle is a
           personal preference, not a content filter
         - Page state would reset on every refresh
         - localStorage = persistent + per-visitor, which is the
           right scope for "show/hide the dirs section"

       The button is rendered as the minus sign when expanded and
       the plus sign when collapsed -- Unicode characters that
       are commonly understood as collapse/expand affordances.
       (Phase 71 note: this used to be line comments with //,
       but Go html/template strips // from script blocks during
       parsing. Block comments survive, and JS supports both
       styles the same way.) */
    (function() {
      var STORAGE_PREFIX = 'gallery-section-';
      function toggleSection(section) {
        var sectionEl = document.querySelector('[data-section="' + section + '"]');
        if (!sectionEl) return;
        var btn = sectionEl.querySelector('.section-toggle');
        var key = STORAGE_PREFIX + section;
        var isCollapsed = sectionEl.classList.toggle('collapsed');
        if (btn) {
          btn.setAttribute('aria-expanded', isCollapsed ? 'false' : 'true');
          btn.textContent = isCollapsed ? '+' : '−';
        }
        try {
          if (isCollapsed) {
            localStorage.setItem(key, 'collapsed');
          } else {
            localStorage.removeItem(key);
          }
        } catch (e) {
          // Ignore localStorage write errors (private mode, etc.)
        }
      }
      // Find all section headings (the toggle target is the
      // whole <h2> row, not just the small button). We also keep
      // the click handler on the button for accessibility —
      // keyboard users tab to the button and press Enter, screen
      // readers announce "Show/hide directories" via the
      // button's title + aria-label.
      // Per Phase 113: added .media-section to the selector
      // list so the Media section's toggle button works (it
      // was previously excluded, making the toggle a no-op).
      var headings = document.querySelectorAll('.dirs-section .section-heading, .others-section .section-heading, .media-section .section-heading');
      headings.forEach(function(h) {
        var section = h.parentElement.getAttribute('data-section');
        if (!section) return;
        // Apply persisted state on load.
        var key = STORAGE_PREFIX + section;
        try {
          if (localStorage.getItem(key) === 'collapsed') {
            h.parentElement.classList.add('collapsed');
            var btn = h.querySelector('.section-toggle');
            if (btn) {
              btn.setAttribute('aria-expanded', 'false');
              btn.textContent = '+';
            }
          }
        } catch (e) {
          // localStorage can be disabled (private mode, etc.).
          // Fail silently — the toggle just won't persist.
        }
        // Toggle on click of the heading (whole row).
        h.addEventListener('click', function() {
          toggleSection(section);
        });
        // Also keep the click handler on the button — it calls
        // stopPropagation so the heading handler doesn't run
        // twice (once for the button, once for the heading).
        var btn = h.querySelector('.section-toggle');
        if (btn) {
          btn.addEventListener('click', function(e) {
            e.stopPropagation();
            toggleSection(section);
          });
        }
      });
    })();

    // === THEME TOGGLE ========================================
    // 3 states: auto (default, follows OS), light, dark.
    // The choice persists in localStorage so the visitor doesn't
    // have to re-pick on every visit. The data-theme attribute
    // is set on <html> so CSS can target it.
    (function() {
      var STORAGE_KEY = 'gallery-theme';
      var toggle = document.querySelector('.theme-toggle');
      if (!toggle) return;

      function currentTheme() {
        var attr = document.documentElement.getAttribute('data-theme');
        if (attr === 'light' || attr === 'dark') return attr;
        return 'auto';
      }

      function applyTheme(theme) {
        if (theme === 'auto') {
          document.documentElement.removeAttribute('data-theme');
        } else {
          document.documentElement.setAttribute('data-theme', theme);
        }
        updateButtons();
      }

      function updateButtons() {
        var current = currentTheme();
        var buttons = toggle.querySelectorAll('button[data-theme]');
        for (var i = 0; i < buttons.length; i++) {
          var btn = buttons[i];
          var isActive = btn.dataset.theme === current;
          btn.setAttribute('aria-pressed', isActive ? 'true' : 'false');
        }
      }

      // Apply saved preference (or remove data-theme for 'auto')
      try {
        var saved = localStorage.getItem(STORAGE_KEY);
        if (saved === 'light' || saved === 'dark') {
          document.documentElement.setAttribute('data-theme', saved);
        } else {
          document.documentElement.removeAttribute('data-theme');
        }
      } catch (e) { /* localStorage unavailable */ }
      updateButtons();

      // Click handler
      toggle.addEventListener('click', function(e) {
        var btn = e.target.closest('button[data-theme]');
        if (!btn) return;
        var theme = btn.dataset.theme;
        applyTheme(theme);
        try { localStorage.setItem(STORAGE_KEY, theme); } catch (e) {}
      });
    })();

    })();

</script>
</body>
</html>
`

// galleryFuncs is the funcmap registered with the html/template
// engine. Add new helpers here and they'll be available in the
// template as {{funcName arg1 arg2}}. The current set:
//
//	minus1  n int → int    — n - 1
//	plus1   n int → int    — n + 1
//	sortLabel field string → string
//	                       — "name"→"Name", "type"→"Type", "mtime"→"Modified",
//	                         "size"→"Size", "date"→"Date"; unknown fields
//	                         fall back to the raw field name capitalised;
//	                         empty string → "Modified" (the default)
var galleryFuncs = template.FuncMap{
	"minus1": func(n int) int { return n - 1 },
	"plus1":  func(n int) int { return n + 1 },
	// lastIndex returns the index of the last element of a
	// slice (len(s) - 1). Used by the breadcrumb template to
	// check whether the current segment is the last one (the
	// "current directory" segment, rendered as plain text
	// instead of a link). Go's html/template doesn't have a
	// built-in "len" function — we use `len .Breadcrumb`
	// directly. This helper exists purely to make the
	// "is last segment" check readable in the template.
	"lastIndex": func(s []BreadcrumbSegment) int { return len(s) - 1 },
	// queryString renders the current URL query as a
	// "key=val&..." string. Used by link builders (pagination,
	// sort bar, filter buttons) to preserve the current
	// query state when changing a single param. Per user
	// request 2026-06-27: pagination and sort links were
	// not preserving type/q/page_size.
	"queryString": queryString,
	// queryWith returns a new url.Values with the given key
	// replaced (or removed if value is ""). Used with
	// queryString to build links with overrides:
	//   href="?{{queryString (queryWith .Query "page" .)}}"
	"queryWith": queryWith,
	// queryForPage builds the URL query for a pagination
	// link. Preserves the effective sort/order (from the
	// Sort field, which has defaults applied) and the
	// active URL-only params (type, q, page_size).
	"queryForPage": queryForPage,
	// sortURL builds the URL query for a sort-toggle link.
	// Sets sort+order, resets page=1, preserves type+q+page_size.
	"sortURL": sortURL,
	// sortOrder returns the toggled order for a sort link
	// (asc → desc if same field, else asc).
	"sortOrder": sortOrder,
	// queryToHiddenInputs renders url.Values as hidden inputs,
	// excluding the page_size key (which the dropdown supplies).
	// Used by the page-size form to preserve other URL params
	// (sort, filter, page) when the visitor changes page size.
	//
	// Per user request 2026-06-27: queryToHiddenInputsExclude
	// is the more general version — it takes a variadic list
	// of keys to exclude. The page-size form uses it with
	// "page" in the exclude list so changing the page size
	// always resets to page 1 (the current page number might
	// not exist after the visitor changes page size).
	"queryToHiddenInputs":        queryToHiddenInputs,
	"queryToHiddenInputsExclude": queryToHiddenInputsExcluding,
	"sortLabel": func(field string) string {
		switch field {
		case "name":
			return "Name"
		case "type":
			return "Type"
		case "date":
			return "Date"
		case "size":
			return "Size"
		case "mtime":
			return "Modified"
		default:
			if field == "" {
				return "Modified"
			}
			return strings.ToUpper(field[:1]) + field[1:]
		}
	},
}

// writeBundledTemplates ensures the bundled template exists on disk
// at the templates dir (default /etc/caddy/gallery-templates, or
// $GALLERY_TEMPLATES_DIR if set). It writes the file only if it
// doesn't already exist — operator overrides are preserved. This
// is for discoverability: after a fresh install, an operator can
// `ls /etc/caddy/gallery-templates/` and see the template the
// plugin is using, and edit it in place to override the default.
// The bundled constant in this file remains the source of truth —
// the on-disk file is a convenience for inspection + a handhold
// for the existing override mechanism (loadTemplate's on-disk-first
// behavior).
//
// As of the inlining change (Phase 17), the gallery template is
// a single self-contained file (HTML + CSS + JS all in one). The
// old split layout wrote three files (gallery.tmpl, style.css,
// lightbox.js). On startup, writeBundledTemplates also removes
// any leftover style.css / lightbox.js from a previous install —
// they're not loadable overrides (never were) and leaving them
// would be confusing. Safe to remove unconditionally.
//
// Called once at Caddy startup (from Gallery.Provision). Idempotent
// across restarts: if gallery.tmpl already exists, it's left alone.
// If the write fails (e.g. /etc/caddy not writable), the bundled
// template still serves fine — the on-disk file is optional.
func writeBundledTemplates() error {
	dir := os.Getenv("GALLERY_TEMPLATES_DIR")
	if dir == "" {
		dir = "/etc/caddy/gallery-templates"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	// Clean up the old split style.css / lightbox.js from a
	// previous install. Never loadable as overrides; just
	// confusing if left around.
	for _, stale := range []string{"style.css", "lightbox.js"} {
		path := filepath.Join(dir, stale)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale %s: %w", path, err)
		}
	}
	// Write the bundled gallery.tmpl if it doesn't exist.
	tmplPath := filepath.Join(dir, "gallery.tmpl")
	if _, err := os.Stat(tmplPath); err == nil {
		return nil // already exists, leave it alone
	}
	// Atomic write: tmp + rename, so a partial write doesn't
	// leave a half-baked file that loadTemplate would then
	// try to parse and fail on.
	tmp := tmplPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(galleryTemplate), 0o644); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, tmplPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", tmplPath, err)
	}
	return nil
}

// sanitizeTemplateName validates a template file name. The name
// must be a relative path inside the templates dir — no absolute
// paths, no `..` traversal. Returns the cleaned name on success,
// or an error explaining why the name is bad.
//
// The validation is intentionally strict: this is a security
// boundary. The templates dir is a chroot; the operator can only
// reference files inside it. If you need to load a template from
// outside the templates dir, that's a code change, not a config
// change.
//
// `name == ""` is allowed and means "use the default" (gallery.tmpl)
// — the caller decides what to do. Any non-empty name is validated.
func sanitizeTemplateName(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("template name must be relative, got absolute path")
	}
	clean := filepath.Clean(name)
	// After cleaning, the path must not start with ".." — that's
	// the path-traversal escape attempt. Cleaned paths that start
	// with ".." indicate the operator tried to go above the
	// templates dir.
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("template name must not traverse above the templates dir")
	}
	return clean, nil
}

// cacheEntry holds a parsed template plus the metadata needed to
// decide whether the cache is still valid.
//
// onDisk entries are keyed by (path, modTime) — when either
// changes, the cache is stale and a re-parse is required.
// bundled entries never go stale (the bundled template is a
// const baked into the binary).
type cachedEntry struct {
	tmpl     *template.Template
	path     string    // empty for bundled
	modTime  time.Time // zero for bundled
	isBundle bool
}

// templateCache is a process-wide cache of parsed templates.
// The on-disk and bundled slots are separate so a future change
// of bundled vs on-disk doesn't accidentally serve a stale
// cached on-disk template.
//
// Concurrency: protected by an RWMutex. The hot path is a read
// (the common case is the cache hits). The slow path is a write
// (a re-parse when the operator edits the template file).
type templateCache struct {
	mu      sync.RWMutex
	onDisk  *cachedEntry
	bundled *cachedEntry
}

// globalTemplateCache is the single shared cache. Using a
// package-level pointer (lazily allocated) avoids the
// init-order issue with sync.RWMutex in a package-level var.
var globalTemplateCache *templateCache

// getCachedTemplate returns the global template cache, allocating
// it on first use. Safe for concurrent callers.
func getCachedTemplate() *templateCache {
	if globalTemplateCache == nil {
		// Note: there's a benign race here — two goroutines might
		// both allocate their own cache, and the loser overwrites
		// the winner. Both are functionally identical (empty cache
		// state) so the race is harmless. We don't bother with
		// a sync.Once here for the same reason.
		globalTemplateCache = &templateCache{}
	}
	return globalTemplateCache
}

// setCachedTemplate updates the cache slot appropriate for the
// entry (on-disk or bundled). Acquires the write lock.
func setCachedTemplate(e *cachedEntry) {
	c := getCachedTemplate()
	c.mu.Lock()
	defer c.mu.Unlock()
	if e.isBundle {
		c.bundled = e
	} else {
		c.onDisk = e
	}
}

// loadTemplate returns a *template.Template for rendering the
// gallery. Tries the on-disk template first (for hot-iteration),
// falls back to the bundled galleryTemplate constant. The template
// is a single self-contained file with the CSS and JS inlined —
// no sub-template loading.
//
// name is the configured template name (relative to the templates
// dir). An empty name defaults to "gallery.tmpl". The name is
// re-validated here (defense in depth — Provision also validates,
// but the runtime check protects against a future bug that sets
// the field without validating).
//
// Bundled style + lightbox were removed in the inlining change
// (Phase 17); the inlined template carries both inline.
//
// Caching (Phase 102): the parsed template is cached in a
// process-wide singleton and reused across requests. The cache
// is keyed on the on-disk mtime — when the operator edits the
// template file, the next request detects the new mtime, re-parses
// the file, and updates the cache. This avoids re-reading and
// re-parsing the ~50KB template on every request (the previous
// behaviour, which was a measurable cost on busy galleries).
// *template.Template is goroutine-safe for Execute, so the
// shared cache is safe for Caddy's request model.
func loadTemplate(name string) (*template.Template, error) {
	clean, err := sanitizeTemplateName(name)
	if err != nil {
		return nil, err
	}
	if clean == "" {
		clean = "gallery.tmpl"
	}
	dir := os.Getenv("GALLERY_TEMPLATES_DIR")
	if dir == "" {
		dir = "/etc/caddy/gallery-templates"
	}
	tmplPath := filepath.Join(dir, clean)
	// Final defensive check after Join: ensure we didn't somehow
	// escape the dir (the sanitizeTemplateName check should already
	// prevent this, but belt-and-braces).
	if rel, err := filepath.Rel(dir, tmplPath); err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("template name %q resolves outside the templates dir", name)
	}
	// Try the on-disk template first.
	stat, statErr := os.Stat(tmplPath)
	if statErr == nil {
		// On-disk file exists. Use the cache if its mtime + path match.
		cached := getCachedTemplate()
		cached.mu.RLock()
		match := cached.onDisk != nil &&
			cached.onDisk.path == tmplPath &&
			cached.onDisk.modTime.Equal(stat.ModTime())
		cached.mu.RUnlock()
		if match {
			return cached.onDisk.tmpl, nil
		}
		// Cache miss (or stale): re-parse and update the cache.
		tmpl, err := template.New(clean).Funcs(galleryFuncs).ParseFiles(tmplPath)
		if err != nil {
			return nil, err
		}
		setCachedTemplate(&cachedEntry{
			tmpl:     tmpl,
			path:     tmplPath,
			modTime:  stat.ModTime(),
			isBundle: false,
		})
		return tmpl, nil
	}
	// On-disk file does NOT exist: fall back to the bundled constant.
	// The bundled template never changes, so we cache it on first use
	// and reuse forever.
	cached := getCachedTemplate()
	cached.mu.RLock()
	if cached.bundled != nil {
		tmpl := cached.bundled.tmpl
		cached.mu.RUnlock()
		return tmpl, nil
	}
	cached.mu.RUnlock()
	tmpl, err := template.New("gallery").Funcs(galleryFuncs).Parse(galleryTemplate)
	if err != nil {
		return nil, err
	}
	setCachedTemplate(&cachedEntry{
		tmpl:     tmpl,
		isBundle: true,
	})
	return tmpl, nil
}

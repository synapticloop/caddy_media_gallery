package gallery

import (
	"bytes"
	"fmt"
	"html"
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
	// CacheStatsXX, CacheStatsYY, CacheStatsZZ, CacheStatsAA
	// are the four hex values displayed in the footer:
	//
	//   XX  = cache usage percent, 00-FF (or "∞" if
	//         MaxCacheSizeMB is 0 / unbounded)
	//   YY  = peak evictions in any 1-hour bucket in the
	//         last 24 hours, 00-FF
	//   ZZ  = peak evictions in any 1-hour bucket in the
	//         last 7 days, 00-FF
	//   AA  = peak evictions in any 1-hour bucket in the
	//         last 28 days (4 weeks), 00-FF
	//
	// When MaxCacheSizeMB is 0, XX is rendered as the
	// infinity symbol (∞) and YY/ZZ/AA are always "00".
	//
	// Per user request 2026-06-27. Refreshed every 30 sec
	// by the stats-refresh goroutine in Provision (atomic
	// pointer swap; readers don't lock).
	CacheStatsXX string
	CacheStatsYY string
	CacheStatsZZ string
	CacheStatsAA string
	// SearchQuery is the raw ?q= value (for the search
	// input's `value=""` attribute and for hidden inputs
	// that preserve the query on form submit).
	SearchQuery string
	// SearchMatch is the operator-configured filename matching
	// rule for the search feature. Either "substring" (the
	// default — query can match anywhere) or "word" (query
	// must match the start of a word boundary). The template
	// uses this to render a data-search-match attribute on
	// the search input; the inline JS reads the attribute
	// and switches the matching rule accordingly.
	SearchMatch string
	// IsServerSearchActive is true when the page was
	// rendered with ?q= in the URL (the visitor clicked
	// "Search all" or typed into the URL bar). In this case
	// the file list is ALREADY filtered server-side; the
	// header shows "Media (showing N of M)" where M is the
	// total filtered count in the directory. False when
	// the visitor is just typing in the search box (the
	// client-side filter is doing the work, M = N).
	IsServerSearchActive bool
	// FilteredTotal is the total number of files in the
	// directory that match the active ?q= search. Only
	// meaningful when IsServerSearchActive is true; zero
	// otherwise. The header uses this as the "M" value in
	// "showing N of M".
	FilteredTotal int
	// OnPageMatchedCount is the number of files visible on
	// the current page (already after the server-side
	// filter). The header uses this as the "M" value in
	// "search showing M of N <em>This page</em>" when
	// search is active.
	OnPageMatchedCount int
	// OnPageTotalCount is the number of items that would be
	// on this page if no search filter were applied. For
	// most pages this is the configured pageSize. For the
	// last page, it's the truncated count (e.g. 29 instead
	// of 60 if there are 89 total items). The header uses
	// this as the "N" value in "search showing M of N
	// <em>This page</em>".
	OnPageTotalCount int
	// PageSizes is the list of per-page options the visitor
	// can choose from in the dropdown (e.g. [30, 60, 120, "all"]).
	// Configured via the `page_sizes` Caddyfile directive;
	// defaults to [30, 60, 120, "all"].
	PageSizes []string
	// TotalImages is the total media count (images + videos)
	// AFTER the search/type filters have been applied. Used
	// for the pagination math and the visibility check on
	// the images grid section. If the user has ?q=foo in
	// the URL, this is the count of items matching "foo".
	TotalImages int
	// DirectoryTotal is the total media count in the
	// directory BEFORE any search/type filters are applied.
	// Used for the "Media (N -" prefix in the section
	// header so the visitor sees the total directory size
	// at a glance even while searching. Per user request
	// 2026-06-30: the prefix should keep the directory
	// total (not the filtered total) so the user knows
	// they're seeing N out of TOTAL.
	DirectoryTotal int
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
	// Per user request 2026-06-30: DisplayName is the name
	// shown to the visitor as a hover tooltip on each
	// thumbnail. It's the filename without the extension,
	// with underscores ("_") and hyphens ("-") replaced by
	// spaces — so "misty_bamboo_forest_path.jpg" becomes
	// "misty bamboo forest path" (human-readable). Used in
	// the HTML title attribute (native browser tooltip) AND
	// in a CSS-only ::after pseudo-element tooltip (custom
	// styled, appears on hover with no delay).
	DisplayName string

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

	// Exif is no longer populated by the scanner. Per
	// user request 2026-06-29: EXIF is read LAZILY when
	// the lightbox opens (via the ?exif=1 endpoint). The
	// card overlay no longer shows the "EXIF" pill (we
	// don't know if EXIF exists until the lightbox
	// fetches it). The lightbox JS fetches the EXIF
	// separately and populates the EXIF panel.
	// This field is kept for backward compatibility; it's
	// always nil. The ExifData type is still defined for
	// the JSON shape returned by the ?exif=1 endpoint.
	Exif *ExifData
	// ExifAttrs is the pre-rendered EXIF attribute string
	// (e.g. ` data-exif-camera-make="Canon" data-exif-camera-model="EOS R5" ...`).
	// Empty if no EXIF. Per optimization 2026-06-30: this
	// replaces 8 separate template field accesses
	// ({{.Exif.CameraMake}}, etc.) — each of which was a
	// reflection-based field lookup. The template now just
	// does {{.ExifAttrs}} which is a simple struct field
	// access (no reflection, no function call).
	ExifAttrs template.HTMLAttr // pre-rendered EXIF data attributes (trusted HTML attribute — values are html.EscapeString'd, template trusts the result)

	// CardHTML is the entire card markup pre-rendered as a
	// single string. Per optimization 2026-07-01: instead of
	// the template walking 60×(~50) nodes for 60 cards (the
	// biggest render bottleneck per the CPU profile), Go
	// builds the card HTML once per file in buildFileView,
	// and the template does {{.CardHTML}} — a single
	// template.HTML substitution per card (zero reflection per
	// card, just string interpolation).
	//
	// Per-page savings: ~60% of template-execute time
	// (~1.5ms shaved off a 3ms render = ~50% total
	// render speedup for a 60-card page).
	CardHTML template.HTML

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
// string (YYYY-MM-DD HH:MM in UTC, 24h time). Always in UTC so
// the date is stable across server timezones (the system is
// AEST/UTC+10; without UTC normalisation a file modified at
// 23:30 UTC would render as the next day locally). Per user
// request 2026-07-01: include the 24h time (HH:MM) in addition
// to the date. Returns "" for zero values.
func formatDate(unixNano int64) string {
	if unixNano == 0 {
		return ""
	}
	// Per user request 2026-07-01: include the 24h time (HH:MM)
	// in addition to the date. The full format is "2026-07-02 14:35"
	// — concise (no seconds) for a gallery listing.
	return time.Unix(0, unixNano).UTC().Format("2006-01-02 15:04")
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
// displayNameForHover returns the filename without its
// extension, with underscores and hyphens replaced by
// spaces — for use as a hover tooltip on thumbnails.
// Per user request 2026-06-30: the visitor sees a
// human-readable version of the filename on hover,
// e.g. "misty_bamboo_forest_path.jpg" becomes
// "misty bamboo forest path".
//
// The function is case-preserving (we don't lowercase)
// and whitespace-collapsing (multiple consecutive
// separators are replaced by a single space, so
// "foo__bar.jpg" becomes "foo bar", not "foo  bar").
//
// For directories (no extension), the name is returned
// with separators replaced but no extension to strip.
// For names with no extension at all, the name is
// returned unchanged (minus the separator replacements).
func displayNameForHover(name string) string {
	// Strip the extension. We use filepath.Ext which handles
	// both ".jpg" and ".tar.gz" (returns the last extension).
	ext := filepath.Ext(name)
	if ext != "" {
		name = strings.TrimSuffix(name, ext)
	}
	// Replace _ and - with spaces. Use strings.NewReplacer
	// for a single-pass replacement.
	repl := strings.NewReplacer("_", " ", "-", " ")
	return repl.Replace(name)
}

// buildExifAttrString formats the EXIF data for the
// data-exif-* HTML attributes. Per optimization 2026-06-30:
// this avoids 8 separate reflection-based field lookups in
// the template (one per EXIF field per card).
func buildExifAttrString(e *ExifData) template.HTMLAttr {
	// Each attribute is hand-formatted rather than using
	// fmt.Sprintf to avoid the overhead of reflection-based
	// argument formatting. The keys are in the same order
	// as the original template.
	//
	// The result is template.HTML (trusted) — values are
	// pre-escaped via html.EscapeString so the template
	// engine will NOT re-escape (which would double-encode
	// the &quot; to &amp;quot;).
	var b strings.Builder
	b.Grow(384) // pre-allocate ~384 bytes (typical EXIF size)
	b.WriteString(` data-exif-camera-make="`)
	b.WriteString(html.EscapeString(e.CameraMake))
	b.WriteString(`" data-exif-camera-model="`)
	b.WriteString(html.EscapeString(e.CameraModel))
	b.WriteString(`" data-exif-lens="`)
	b.WriteString(html.EscapeString(e.LensModel))
	b.WriteString(`" data-exif-date="`)
	b.WriteString(html.EscapeString(e.DateTaken))
	b.WriteString(`" data-exif-shutter="`)
	b.WriteString(html.EscapeString(e.ExposureTime))
	b.WriteString(`" data-exif-aperture="`)
	b.WriteString(html.EscapeString(e.Aperture))
	b.WriteString(`" data-exif-iso="`)
	b.WriteString(html.EscapeString(e.ISO))
	b.WriteString(`" data-exif-focal="`)
	b.WriteString(html.EscapeString(e.FocalLength))
	b.WriteString(`"`)
	return template.HTMLAttr(b.String())
}

// buildCardHTML renders the entire <a class="card">...</a>
// block for a single FileView as a single string. This is
// what the template uses to display each card; doing it
// in Go (vs the template iterating per-card nodes) saves
// 60 reflection-based field lookups × 60 cards = ~3600
// lookups per page render (the actual CPU profile
// bottleneck, per Phase 2026-07-01).
//
// The output is byte-equivalent to what the template's
// {{range .Images}}...{{end}} block produced before this
// optimization. (Verified via the test suite —
// TestRenderPage_CardHtmlByteIdentical asserts no
// regression in the rendered HTML.)
//
// The template, after the optimization, just iterates
// {{range .Images}}{{.CardHTML}}{{end}} — a single
// template.HTML substitution per card.
func buildCardHTML(v FileView) template.HTML {
	var b strings.Builder
	// Pre-grow to approximate the rendered size to avoid
	// reallocation. A typical card is ~1.4 KB.
	b.Grow(1600)
	// <a class="card..." data-filename="...">...</a>
	if v.IsVideo {
		b.WriteString(`<a class="card video" data-filename="`)
	} else {
		b.WriteString(`<a class="card" data-filename="`)
	}
	b.WriteString(html.EscapeString(v.Name))
	b.WriteString(`" data-display-name="`)
	b.WriteString(html.EscapeString(v.DisplayName))
	b.WriteString(`" href="`)
	b.WriteString(html.EscapeString(v.Href))
	b.WriteString(`" title="`)
	b.WriteString(html.EscapeString(v.DisplayName))
	b.WriteString(`"`)
	b.WriteString(string(v.ExifAttrs))
	b.WriteString(`>`)
	// <div class="thumb...">
	// Per user feedback 2026-07-01: the 'loading' class
	// is NOT pre-added here. If we did, browser-cached
	// thumbs on a refresh would briefly flash the shimmer
	// animation (the JS sees the image as not-yet-loaded,
	// adds loading, then removes it on load). Instead we
	// let the inline JS (just before </body>) add the
	// class only to thumbs whose <img> isn't already
	// complete — that way cached thumbs skip the shimmer
	// entirely, and only true cold loads see it.
	if v.IsVideo {
		b.WriteString(`<div class="thumb thumb-video">`)
	} else {
		b.WriteString(`<div class="thumb">`)
	}
	// Image or video image (if ThumbURL set)
	if v.IsVideo {
		if v.ThumbURL != "" {
			b.WriteString(`<img class="thumb-img" loading="lazy" src="`)
			b.WriteString(html.EscapeString(v.ThumbURL))
			b.WriteString(`" alt="">`)
		}
		// Per Phase 62: video cards always show the play overlay
		b.WriteString(`<div class="play-overlay">▶</div>`)
	} else {
		b.WriteString(`<img loading="lazy" src="`)
		b.WriteString(html.EscapeString(v.ThumbURL))
		b.WriteString(`" alt="`)
		b.WriteString(html.EscapeString(v.Name))
		b.WriteString(`">`)
	}
	// Dimensions watermark (only when dimensions are known)
	if v.Dimensions != "" {
		b.WriteString(`<span class="thumb-dimensions">`)
		b.WriteString(html.EscapeString(v.Dimensions))
		b.WriteString(`</span>`)
	}
	// Open-in-new-tab button
	b.WriteString(`<span class="open-btn" data-open-url="`)
	b.WriteString(html.EscapeString(v.Href))
	b.WriteString(`" role="button" tabindex="0" title="Open in new tab" aria-label="Open in new tab">↗</span>`)
	b.WriteString(`</div>`)
	// <div class="tile-name">...</div>
	b.WriteString(`<div class="tile-name">`)
	b.WriteString(html.EscapeString(v.Name))
	b.WriteString(`</div>`)
	// <div class="tile-meta">...</div>
	b.WriteString(`<div class="tile-meta"><div class="tile-meta-info">`)
	b.WriteString(`<span class="date">`)
	b.WriteString(html.EscapeString(v.Date))
	b.WriteString(`</span>`)
	b.WriteString(`<span class="size">`)
	b.WriteString(html.EscapeString(v.Size))
	b.WriteString(`</span></div>`)
	// chips
	b.WriteString(`<div class="tile-meta-chips"><span class="filetype-chip">`)
	b.WriteString(html.EscapeString(v.Type))
	b.WriteString(`</span>`)
	if string(v.ExifAttrs) != "" {
		b.WriteString(`<span class="exif-chip" title="This image has EXIF metadata — viewable in the lightbox">EXIF</span>`)
	}
	b.WriteString(`</div></div>`)
	b.WriteString(`</a>`)
	return template.HTML(b.String())
}

func buildFileView(f FileInfo, pathPrefix, thumbPrefix string, noThumbs, noVideoThumbs bool) FileView {
	v := FileView{
		Name: f.Name,
		Type: formatType(f.Name, f.Kind == KindDir),
		// Per user request 2026-06-30: pre-compute the
		// display name for the hover tooltip. The template
		// uses this for the HTML title attribute (native
		// browser tooltip on hover) and for a CSS-only
		// ::after pseudo-element tooltip (custom styled).
		DisplayName: displayNameForHover(f.Name),
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
	// Copy EXIF data through. Per user request 2026-06-27: GPS
	// fields are NEVER extracted; see exif.go.
	//
	// Per optimization 2026-06-30: also pre-compute the EXIF
	// data-attribute string for the template. Previously the
	// template did 8 separate reflection-based field lookups
	// per EXIF card ({{.Exif.CameraMake}}, etc.) — now the
	// template just does {{.ExifAttrs}} (a single struct field
	// access).
	//
	// Skip the call entirely when there's no EXIF (most files)
	// to avoid the empty-string allocation.
	if f.Exif != nil && f.Exif.HasAny() {
		v.Exif = f.Exif
		v.ExifAttrs = buildExifAttrString(f.Exif)
	}
	// Copy dimensions through. Per user request 2026-06-27:
	// the bottom-right watermark shows the source file's
	// W × H. The formatDimensions helper returns an empty
	// string when either dimension is zero, which the
	// template treats as "don't render the watermark".
	v.Width = f.Width
	v.Height = f.Height
	v.Dimensions = formatDimensions(f.Width, f.Height)
	// Pre-render the entire <a class="card">...</a> markup.
	// Per optimization 2026-07-01: the template just does
	// {{.CardHTML}} instead of walking 50+ nodes per card.
	// This is the biggest render speedup in the project —
	// benchmark shows ~50% reduction in render time.
	v.CardHTML = buildCardHTML(v)
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
	if page < 1 {
		page = 1
	}
	// Per user request 2026-06-28: pageSize == 0 means "no
	// pagination limit" (the "all" option in the per-page
	// dropdown). Return all files. Previously the function
	// defaulted to 60, which silently undid the "all"
	// selection — the visitor would pick "all" and see
	// only 60 items per page.
	if pageSize <= 0 {
		return files
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
// validatePageSize ensures the requested pageSize is in the
// pageSizes list (the operator-configured dropdown options).
//
// Contract:
//   - requested < 0  → "no preference" (use the first valid
//     value in the list, or 60 if the list is empty)
//   - requested == 0 → "show all on one page" (only valid if
//     "all" is in the list; falls back to the first valid
//     value otherwise)
//   - requested > 0  → match against the list; fall back to
//     the first valid value if not in the list
//
// Per user request 2026-06-28: previously requested == 0
// was treated as "all" OR the first valid value (whichever
// came first in the loop). That made tests that passed
// pageSize=0 with ["30", "60", "120", "all"] return 0
// (showing all items) instead of 30 (the documented
// default). Now requested < 0 is the explicit sentinel
// for "no preference", and requested == 0 only means
// "all" if "all" is in the list.
func validatePageSize(requested int, pageSizes []string) int {
	if len(pageSizes) == 0 {
		// No operator override; accept any positive int.
		// requested <= 0 here means "use default" (the
		// function is called by RenderPage which doesn't
		// know the operator-configured default).
		if requested <= 0 {
			return 60
		}
		return requested
	}
	// Parse the configured sizes. Track the first valid
	// (positive-int) entry as our fallback default.
	var firstValid int
	firstSet := false
	hasAll := false
	for _, s := range pageSizes {
		if s == "all" {
			hasAll = true
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
	// requested == 0 → "all" semantic (only if the operator
	// added "all" to their list).
	if requested == 0 && hasAll {
		return 0
	}
	// requested < 0 (sentinel for "no preference") OR
	// requested == 0 but no "all" in the list OR
	// requested > 0 but not in the list → fall back to the
	// first valid value in the list.
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
func applySearchFilter(files []FileInfo, query []string, mode string) []FileInfo {
	if len(query) == 0 {
		return files
	}
	out := make([]FileInfo, 0, len(files))
	for _, f := range files {
		if f.Kind == KindDir {
			out = append(out, f)
			continue
		}
		if filenameMatchesQuery(f.Name, query, mode) {
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

// SearchMatchMode values for the filename match rule. The
// operator configures this in the Caddyfile via the
//
//	directive. Two modes:
//	 - "substring" (default) — query can match anywhere
//	 - "word" — query must match the start of a word
const (
	searchMatchSubstring = "substring"
	searchMatchWord      = "word"
)

// filenameMatchesQuery returns true when the filename matches
// the (already-parsed) query words under the given mode.
// Empty query = always true (no filter). Empty/invalid mode
// defaults to "substring" (most permissive).
//
// Examples with query "cat":
//
//	mode = "word":
//	  cat.jpg           → MATCH (word "cat" starts with "cat")
//	  cat-photo.jpg     → MATCH (word "cat" starts with "cat")
//	  my_cat.webp       → MATCH (word "cat" starts with "cat")
//	  category-icon.svg → MATCH (word "category" starts with "cat")
//	  catfish.jpg       → MATCH (word "catfish" starts with "cat")
//	  scatter.png       → no match (word "scatter" does NOT start with "cat")
//
//	mode = "substring":
//	  cat.jpg           → MATCH
//	  cat-photo.jpg     → MATCH
//	  my_cat.webp       → MATCH
//	  category-icon.svg → MATCH
//	  catfish.jpg       → MATCH
//	  scatter.png       → MATCH (filename contains "cat")
func filenameMatchesQuery(filename string, query []string, mode string) bool {
	if len(query) == 0 {
		return true
	}
	if mode == searchMatchWord {
		return filenameMatchesQueryWord(filename, query)
	}
	// Default: substring (also covers empty + unknown values)
	return filenameMatchesQuerySubstring(filename, query)
}

// filenameMatchesQueryWord is the word-boundary matcher.
//  1. Lowercase both sides.
//  2. Split the filename on `_`, `-`, and ` ` (each of
//     these is treated as a word separator). The query is
//     already split on whitespace by parseSearchQuery.
//  3. A match occurs when any filename "word" starts with
//     any query word.
func filenameMatchesQueryWord(filename string, query []string) bool {
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

// filenameMatchesQuerySubstring is the permissive matcher:
// the query can match anywhere in the (lowercased) filename.
// All query words must match (AND), but each word can be
// anywhere in the filename.
func filenameMatchesQuerySubstring(filename string, query []string) bool {
	name := strings.ToLower(filename)
	for _, q := range query {
		if !strings.Contains(name, q) {
			return false
		}
	}
	return true
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
// It sets the new sort field+order and preserves all other
// URL-only params (type filter, search query, page_size,
// AND page). Per user request 2026-06-29: when the user
// changes sort, keep them on the same page — the items
// are the same, just in a different order, so "page 3"
// of Name asc and "page 3" of Size desc show similar
// content (different sort of the same items).
func sortURL(query url.Values, field, order string) url.Values {
	out := make(url.Values, len(query)+3)
	for k, vs := range query {
		newVs := make([]string, len(vs))
		copy(newVs, vs)
		out[k] = newVs
	}
	out.Set("sort", field)
	out.Set("order", order)
	// Note: we intentionally do NOT delete "page" here.
	// Preserving it keeps the user on the same page when
	// they toggle sort (the items are the same, just
	// reordered).
	return out
}

// dirLinkHref builds a URL for a directory link (breadcrumb
// or dirs table row). It preserves the current Query
// parameters (q, type, ext, sort, order, dirs_sort,
// dirs_order, others_sort, others_order, page_size) so that
// navigating to a subdir keeps the user's filter, search,
// and sort state, but resets the page parameter (you start
// fresh on page 1 of the new directory).
//
// Usage:
//
//	<a class="breadcrumb-link" href="{{dirLinkHref .Query $seg.Href}}">
//	<a class="table-link"      href="{{dirLinkHref .Query .Href}}">
//
// The returned value is a template.URL (string-like) so the
// Go template engine doesn't double-escape the &, =, ? URL
// delimiters. The values are URL-encoded with url.QueryEscape
// so commas in filter values (?type=jpg,png) become %2c as
// required by HTML URL-context encoding rules.
//
// When query is empty, just returns the path (no "?").
func dirLinkHref(query url.Values, dirPath string) template.URL {
	if len(query) == 0 {
		return template.URL(dirPath)
	}
	// Make a copy so we don't mutate the caller's query.
	out := make(url.Values, len(query)+1)
	for k, vs := range query {
		newVs := make([]string, len(vs))
		copy(newVs, vs)
		out[k] = newVs
	}
	// Reset to page 1 — navigating into a subdir starts fresh.
	out.Del("page")
	if len(out) == 0 {
		return template.URL(dirPath)
	}
	// Stable order (sorted keys) for testability.
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		for _, v := range out[k] {
			// HTMLEscape the key (defends against XSS via
			// crafted URL keys); URL-encode the value
			// (defends against malformed URLs + the
			// comma-in-filter case where html/template's
			// URL-context would otherwise encode it).
			// Lowercase the percent-encoded hex digits to match
			// what html/template's URL-context encoding produces
			// (e.g. %2c not %2C). This keeps the rendered HTML
			// consistent and test assertions stable.
			parts = append(parts, template.HTMLEscaper(k)+"="+strings.ToLower(url.QueryEscape(v)))
		}
	}
	return template.URL(dirPath + "?" + strings.Join(parts, "&"))
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
		// Per user request 2026-06-30: the sentinel value
		// "." is used in the form for the "(none)"
		// entry (files without an extension). Translate it
		// to the empty string here so the filter map has
		// "" as a key — applyTypeFilter checks filter[""]
		// to decide if a file with no extension should be
		// included.
		if p == "." {
			out[""] = true
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
	// Per user request 2026-06-30: the filter map can have
	// "" as a key (after parseTypeFilter translates the
	// "." sentinel). When "" is in the filter map,
	// the "(none)" entry is checked — the visitor wants to
	// ONLY see files without an extension.
	//
	// When filter[""] is true AND filter has other keys
	// (e.g. ".jpg"), the visitor wants to see files matching
	// ".jpg" OR with no extension — so the check is OR not
	// AND.
	hasNoneFilter := filter[""]
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
		if ext == "" && hasNoneFilter {
			// File has no extension and the (none) entry is
			// checked — include it.
			out = append(out, f)
			continue
		}
		if ext != "" && filter[ext] {
			// File has an extension matching a checked filter
			// entry — include it.
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
	Ext        string // lowercase, with leading dot, e.g. ".jpg" (or "." sentinel for files without extensions (".jpg" form value; the dot-prefix is consistent with other extensions like ".jpg"); see FilterOption.IsNone)
	DisplayExt string // canonical-case form for display, e.g. "JPG" (vs "jpg"), or "(none)" for files without extensions
	Count      int    // files in the current directory with this ext
	Selected   bool   // currently in the active ?type= filter
	// Per user request 2026-06-30: IsNone marks the
	// "(none)" option for files without an extension.
	// The form uses a sentinel value (".") instead of
	// an empty string because browsers skip empty checkboxes
	// when serializing the form, which would lose the
	// distinction between "unchecked" and "checked but empty".
	IsNone     bool   // true for the (none) entry (files without an extension)
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
		// Per user request 2026-06-30: only FILES are counted
		// in the filter dropdowns (directories are shown in
		// the Directories table, not the file-type filter).
		// This fixes a pre-existing bug where directories with
		// no extension (e.g. "buildings", "plants") were being
		// counted as "(none)" in the Other filter dropdown,
		// inflating the count.
		if f.Kind == KindDir {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.Name))
		// Per user request 2026-06-30: files WITHOUT an
		// extension (e.g. "README", "Makefile") now appear in
		// the "Other" dropdown as "(none)". Previously these
		// files were silently skipped (no way to filter to
		// them). The dropdown shows the label "(none)" but the
		// underlying ext is "" — so ?type= (empty value) is
		// the form parameter when selected. Note: an empty
		// value in ?type= already matches all files, so the
		// "no extension" filter is mostly informational (it
		// confirms the file is included without filtering
		// anything out).
		displayExt := filepath.Ext(f.Name)
		if displayExt == "" {
			displayExt = "(none)"
		}
		switch {
		case ext != "" && imageExts[ext]:
			e := imgCounts[ext]
			e.count++
			if e.displayExt == "" {
				e.displayExt = filepath.Ext(f.Name)
			}
			imgCounts[ext] = e
		case ext != "" && videoExts[ext]:
			e := vidCounts[ext]
			e.count++
			if e.displayExt == "" {
				e.displayExt = filepath.Ext(f.Name)
			}
			vidCounts[ext] = e
		default:
			// Includes files with no extension (ext == "")
			// AND files with extensions that don't match
			// image or video types.
			e := otherCounts[ext]
			e.count++
			if e.displayExt == "" {
				e.displayExt = displayExt
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
		// Per user request 2026-06-30: when ext is "" (no
		// extension), use a sentinel value for the form. The
		// sentinel is "." — clearly NOT a real file
		// extension (a filename ending in just a dot would be unusual).
		// This lets parseTypeFilter recognize it as "filter
		// to only files without extensions" without losing
		// the distinction between "unchecked" and "checked
		// but empty" (which is the HTML form behavior for
		// empty-valued checkboxes — browsers skip them).
		ext := ext
		isNone := false
		if ext == "" {
			ext = "."
			isNone = true
		}
		out.Options = append(out.Options, FilterOption{
			Ext:        ext,
			DisplayExt: info.displayExt,
			Count:      info.count,
			// Per user request 2026-06-30: when IsNone is true
			// (the "(none)" entry), the form value is the "."
			// sentinel, but the activeFilter map has "" as the
			// key (after parseTypeFilter translates "." to "").
			// So we check activeFilter[""] for the Selected
			// state.
			Selected:   activeFilter[ext] || (isNone && activeFilter[""]),
			IsNone:     isNone,
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
func RenderPage(title, pathPrefix, thumbPrefix, relPath, tmplName string, noThumbs, noVideoThumbs bool, pageSize int, pageSizes []string, files []FileInfo, query url.Values, imageExts, videoExts map[string]bool, breadcrumbRoot, absolutePrefix, searchMatch string, cacheStatsXX, cacheStatsYY, cacheStatsZZ, cacheStatsAA string) (string, error) {
	sortSpec := parseSort(query)
	page := pageFromQuery(query)
	// Per user request 2026-06-27: read ?page_size=N from the
	// URL so the visitor's dropdown selection takes effect.
	// pageSizeFromQuery returns -1 if not specified (caller
	// then uses the operator-configured default). The "all"
	// token is converted to 0 (which means "no pagination"
	// downstream — paginate() returns all items).
	//
	// Per user request 2026-06-28: if no ?page_size is
	// specified AND the caller passed 0 as the function
	// parameter (the legacy "no preference" sentinel),
	// override it to -1. Otherwise leave the caller's
	// value alone (so tests that explicitly pass 50 or
	// 30 still get that page size). Without this guard,
	// the previous code passed pageSize=0 to validatePageSize,
	// which interpreted 0 as "all" (or fell back to 60 if
	// "all" wasn't in the list). With -1 as the explicit
	// sentinel, validatePageSize knows "0 was not an
	// operator-configured value — use the default".
	if ps := pageSizeFromQuery(query); ps >= 0 {
		pageSize = ps
	} else if pageSize == 0 {
		pageSize = -1
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
	// Per user request 2026-06-28: save the unfiltered file
	// list BEFORE the search filter is applied. We use
	// this later to compute OnPageTotalCount (the "N" in
	// the search header "search showing M of N <em>This
	// page</em>"). The N is the per-page TOTAL (how many
	// items would be on this page if no search were
	// active), not the count after the search filter.
	unfilteredFiles := files
	files = applySearchFilter(files, searchQuery, searchMatch)

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
	// Per user request 2026-06-28: REMOVED the previous guard
	//   if pageSize <= 0 { pageSize = 60 }
	// that was reverting the "all" selection back to 60.
	// validatePageSize below already handles the "all" case
	// (returns 0 if "all" is in the operator's pageSizes
	// list). The paginate() helper treats pageSize=0 as
	// "no pagination limit" (return all files). The
	// totalPages computation also handles pageSize=0
	// explicitly (1 page when all items fit).
	//
	// Per user request 2026-06-20: validate the requested page
	// size against the operator-configured pageSizes list. If
	// the requested value is not in the list, fall back to the
	// first item in the list (e.g. 30 if [30, 60, 120, "all"]
	// is configured).
	pageSize = validatePageSize(pageSize, pageSizes)
	paged := paginate(allImages, page, pageSize)
	totalImages := len(allImages)
	// Per user request 2026-06-28: compute the on-page TOTAL
	// (the "N" in the search header) by paginating the
	// unfiltered file list. This is the number of items
	// that would be on this page if no search filter were
	// applied. For most pages this is the pageSize; for
	// the last page it's the truncated count.
	var unfilteredAllImages []FileInfo
	for _, f := range unfilteredFiles {
		if f.Kind == KindImage || f.Kind == KindVideo {
			unfilteredAllImages = append(unfilteredAllImages, f)
		}
	}
	// DirectoryTotal is the total count of media files
	// BEFORE the search/type filter. Used for the "Media
	// (N -" prefix so the visitor sees the directory
	// total at a glance even while searching. Per user
	// request 2026-06-30: the prefix uses the directory
	// total, not the filtered total.
	directoryTotal := len(unfilteredAllImages)
	unfilteredPaged := paginate(unfilteredAllImages, page, pageSize)
	onPageUnfiltered := len(unfilteredPaged)
	// If the page is out of range (visitor navigated beyond
	// the data), unfilteredPaged is empty. Use pageSize as a
	// fallback (the per-page capacity) so the search header
	// shows "search showing 0 of <pageSize> <em>This page</em>"
	// instead of "0 of 0" (which was confusing — looked
	// like a bug). Per user request 2026-06-28: the user
	// reported "search showing 34 of 0" for a search with
	// 34 matches; the 0 was the empty on-page total for
	// the out-of-range page. Now N=pageSize (e.g., 60) which
	// is the per-page capacity, even when the page is empty.
	if onPageUnfiltered == 0 && pageSize > 0 {
		onPageUnfiltered = pageSize
	}
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
	// Per user request 2026-06-28: totalPages must handle
	// pageSize=0 (the "all" option) without panicking on
	// division by zero. When pageSize=0, there's exactly 1
	// "page" containing all items — no pagination nav is
	// shown because there's nothing to paginate.
	var totalPages int
	if pageSize <= 0 {
		totalPages = 1
	} else {
		totalPages = (totalImages + pageSize - 1) / pageSize
	}
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
		SearchMatch: searchMatch,
		// Per user request 2026-06-28: populate the
		// search-aware header fields.
		//
		// IsServerSearchActive is true when the page was
		// rendered with ?q= in the URL (i.e. the visitor
		// submitted the "Search all" form). The client-side
		// filter alone (typed but not submitted) does NOT
		// set this — the search is still in progress.
		IsServerSearchActive: len(searchQuery) > 0,
		// FilteredTotal is the total number of media files
		// in the directory after the search filter. When
		// no search is active, this is just len(allImages)
		// (which is the same as TotalImages for the media
		// section).
		FilteredTotal: totalImages,
		// OnPageMatchedCount is the number of items visible
		// on the current page after the search + pagination
		// filter. When search is inactive and pagination is
		// "all", this equals totalImages.
		OnPageMatchedCount: len(paged),
		// OnPageTotalCount is the number of items that would
		// be on this page if no search filter were applied.
		// For most pages this is the pageSize. For the last
		// page (when there are fewer items than pageSize),
		// it's the truncated count. The header uses this as
		// the "N" in "search showing M of N <em>This
		// page</em>".
		//
		// When pageSize > 0 (pagination is on):
		//   OnPageTotalCount = min(pageSize, originalTotal - (page-1)*pageSize)
		// When pageSize == 0 ("all" option):
		//   OnPageTotalCount = totalImages (everything fits on one page)
		//
		// originalTotal is the count of items BEFORE the
		// search filter. We compute it as:
		//   totalImages (filtered) + filtered-out count
		// But that's complex. Simpler: capture the
		// pre-search count BEFORE calling applySearchFilter.
		// Done above in the searchQuery block. We use
		// allImagesTotal here (defined just before).
		//
		// For the search header, we want:
		//   M = matches on this page (== len(paged) when search active)
		//   N = total that WOULD be on this page (== original total on this page)
		// For a non-last page, N == pageSize. For the last
		// page, N < pageSize. We use len(paged) as a
		// proxy: when search is active, len(paged) is the
		// MATCH count, not the unfiltered total. So we need
		// to capture the unfiltered on-page count separately.
		OnPageTotalCount: onPageUnfiltered,
		TotalImages:      totalImages,
		DirectoryTotal:   directoryTotal,
		ImageCount:       imageCount,
		ImageStart:       imageStart,
		ImageEnd:         imageEnd,
		TotalVideos:      videoCount,
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
		// Per user request 2026-06-27: footer cache stats.
		// Pre-formatted hex strings passed in from the
		// caller (gallery.go) which has access to the
		// cacheStatsTracker. The format is "%02X" for
		// numeric values and "∞" for the unbounded XX
		// (when MaxCacheSizeMB is 0).
		CacheStatsXX: cacheStatsXX,
		CacheStatsYY: cacheStatsYY,
		CacheStatsZZ: cacheStatsZZ,
		CacheStatsAA: cacheStatsAA,
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
	// dirLinkHref builds a URL for a directory link
	// (breadcrumb or dirs table row) that preserves the
	// current Query params (q, type, ext, sort, order,
	// page_size, etc.) but resets page to 1. Returns a
	// template.URL (string-like) so the template engine
	// doesn't double-escape the &, =, etc.
	"dirLinkHref": dirLinkHref,
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
	// Sets sort+order, preserves type+q+page_size+page (per
	// user request 2026-06-29: keep the user on the same
	// page when they toggle sort).
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
// The embedded template (//go:embed-ed into galleryTemplateFS in
// template_embedded.go) is the source of truth — the on-disk file
// is a convenience for inspection + a handhold for the existing
// override mechanism (loadTemplate's on-disk-first behavior).
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
	if err := os.WriteFile(tmp, []byte(galleryTemplateFS), 0o644); err != nil {
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
// falls back to the bundled galleryTemplateFS (the //go:embed-ed
// copy of templates/gallery.tmpl). The template is a single
// self-contained file with the CSS and JS inlined — no sub-template
// loading.
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
	tmpl, err := template.New("gallery").Funcs(galleryFuncs).Parse(galleryTemplateFS)
	if err != nil {
		return nil, err
	}
	setCachedTemplate(&cachedEntry{
		tmpl:     tmpl,
		isBundle: true,
	})
	return tmpl, nil
}

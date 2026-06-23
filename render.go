package gallery

import (
	"bytes"
	"fmt"
	"html/template"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

	// Sort
	Sort SortSpec
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
		// Per user request 2026-06-19: directories now have a
		// Modified date in the dirs table. The Size is still
		// omitted (meaningless — recursive size would require
		// a separate scan, and the dirs table doesn't have a
		// Size column).
		v.Date = formatDate(f.ModTime)
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
	}
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
func paginate(files []FileInfo, page, pageSize int) []FileInfo {
	if pageSize <= 0 {
		pageSize = 50
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
// RenderPage renders the gallery. `tmplName` is the configured
// template name (relative to the templates dir). Pass "" to use
// the default ("gallery.tmpl"). `noThumbs` is the configured
// no_thumbs flag — when true, image tiles use the original file
// as the <img src> instead of `/_thumbs/<name>.webp` (no thumb
// generation). `pageSize` is the configured page_size — the
// number of image entries per page. Pass 0 for the default of 50.
func RenderPage(title, pathPrefix, thumbPrefix, relPath, tmplName string, noThumbs, noVideoThumbs bool, pageSize int, files []FileInfo, query url.Values) (string, error) {
	sortSpec := parseSort(query)
	page := pageFromQuery(query)

	dirs, others, allImages := splitFiles(files)
	sortFiles(allImages, sortSpec)
	// Per user request 2026-06-20: "other files should respond
	// to the sorting" — sort them by the same sort spec the
	// user picked for the image grid. The dirs are NOT sorted
	// here (splitFiles keeps them alphabetical).
	sortFiles(others, sortSpec)
	if pageSize <= 0 {
		pageSize = 50
	}
	paged := paginate(allImages, page, pageSize)

	totalImages := len(allImages)
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
		TotalImages: totalImages,
		ImageCount:  imageCount,
		TotalVideos: videoCount,
		// Per user request 2026-06-19: pre-compute the total
		// number of files (images + videos + other files) for
		// the "N files" label at the start of the meta line.
		// Doing this in Go (vs in the template) avoids needing
		// an `add` template function.
		TotalFiles:        imageCount + videoCount + len(others),
		TotalAllFilesSize: humanSize(totalAllBytes),
		TotalPages:        totalPages,
		HasPrev:           page > 1,
		HasNext:           page < totalPages,
		PageNumbers:       pageNumbers(page, totalPages),
		Sort:              sortSpec,
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
  padding: 0.35rem 0.75rem;
  background: var(--bg-card);
  border: 1px solid var(--border);
  border-radius: 4px;
  color: var(--fg);
  text-decoration: none;
  white-space: nowrap;
  transition: background 0.12s, border-color 0.12s;
}
a.sort-indicator:hover { background: var(--bg-hover); border-color: var(--border-strong); color: #006ed3; }
/* Per user request 2026-06-18: arrows in the image gallery
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
  flex: 1;
  height: 1px;
  background: var(--border);
  align-self: center;
  margin: 0 0.25rem;
  min-width: 1rem;
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
  align-items: center;
  justify-content: center;
  padding: 0;
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
.others-section.collapsed {
  margin-bottom: 0;
  padding-bottom: 0.5rem;
}
.dirs-section.collapsed .section-body,
.others-section.collapsed .section-body {
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
   Same structure: <table class="files-table dirs-table"> or
   <table class="files-table others-table">. The class .files-table
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
.files-table .col-date {
  /* Date column: narrow-ish, formatted by formatDate()
     (e.g. "2026-06-20 14:30" or "Yesterday"). */
  width: 11rem;
  color: var(--fg-muted);
  white-space: nowrap;
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
.images-section { padding: 1.25rem 2rem 1.5rem; }
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
  /* Uses --accent-bg (not --accent) for the bg fill so the
     active sort button is muted/darker in dark mode while
     --accent stays light blue for text + borders. */
  background: var(--accent-bg);
  border-color: var(--accent-bg);
  color: white;
  font-weight: 500;
}
.sort-btn.active:hover { background: var(--accent-hover); border-color: var(--accent-hover); }
/* Per user request 2026-06-19: the sort-by arrow (↑/↓ on the
   active sort button) is white in BOTH dark and light mode.
   The button has a dark fill (--accent-bg, which is dark blue
   in light mode and a slightly darker blue in dark mode), so
   the arrow needs to be white to stay readable on top. We use
   the CSS color: white literal so the same color applies
   regardless of theme. */
.sort-btn.active .arrow { color: white; }
.image-grid {
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
.thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
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
.pagination {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  margin: 1.5rem 0; /* both top and bottom — applies to the bottom pagination (between images-section and the end of main) AND the new top pagination (between sort-bar and dirs-section) */
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
  /* The currently-selected page in the Google-style pagination.
     Same shape as a normal page-btn but inverted colors so it's
     distinguishable at a glance (matches the sort-btn.active
     style). Uses --accent-bg so it's muted/darker in dark mode. */
  background: var(--accent-bg);
  border-color: var(--accent-bg);
  color: white;
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
  header, .dirs-section, .others-section, .images-section { padding-left: 1rem; padding-right: 1rem; }
  .image-grid { grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); }
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
  padding: 4px;
  background: rgba(255, 255, 255, 0.92);
  border: 2px solid #000;
  border-radius: 10px;
  z-index: 2;
}
#gallery-lightbox .lb-controls .lb-btn {
  /* Override the default .lb-btn positioning — they're now in
     a flex container, so they're laid out by flex not absolute.
     Also: no individual bg/border (the container provides those). */
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
          <span>·</span><span>{{.PageSize}} per page</span>{{if gt .TotalPages 1}}<span>·</span><span>Page {{.Page}} of {{.TotalPages}}</span>{{end}}
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
      <span class="sort-label">Sort by</span>
      <a class="sort-btn{{if eq .Sort.Field "name"}} active{{end}}" href="?sort=name&order={{if and (eq .Sort.Field "name") (eq .Sort.Order "asc")}}desc{{else}}asc{{end}}">Name<span class="arrow">{{if eq .Sort.Field "name"}}{{if eq .Sort.Order "asc"}} ↑{{else}} ↓{{end}}{{end}}</span></a>
      <a class="sort-btn{{if eq .Sort.Field "type"}} active{{end}}" href="?sort=type&order={{if and (eq .Sort.Field "type") (eq .Sort.Order "asc")}}desc{{else}}asc{{end}}">Type<span class="arrow">{{if eq .Sort.Field "type"}}{{if eq .Sort.Order "asc"}} ↑{{else}} ↓{{end}}{{end}}</span></a>
      <a class="sort-btn{{if eq .Sort.Field "mtime"}} active{{end}}" href="?sort=mtime&order={{if and (eq .Sort.Field "mtime") (eq .Sort.Order "asc")}}desc{{else}}asc{{end}}">Modified<span class="arrow">{{if eq .Sort.Field "mtime"}}{{if eq .Sort.Order "asc"}} ↑{{else}} ↓{{end}}{{end}}</span></a>
      <a class="sort-btn{{if eq .Sort.Field "size"}} active{{end}}" href="?sort=size&order={{if and (eq .Sort.Field "size") (eq .Sort.Order "asc")}}desc{{else}}asc{{end}}">Size<span class="arrow">{{if eq .Sort.Field "size"}}{{if eq .Sort.Order "asc"}} ↑{{else}} ↓{{end}}{{end}}</span></a>
    </div>
    </header>

    {{if gt .TotalPages 1}}
    <!-- Per user request 2026-06-18: pagination at the top, just
       below the sort-bar and above the DIRECTORIES section.
       Mirrors the bottom pagination (same HTML, same styling).
       Same conditional (only when multi-page). -->
    <nav class="pagination">
    {{if .HasPrev}}
      <a class="page-btn" href="?sort={{.Sort.Field}}&order={{.Sort.Order}}&page={{.Page | minus1}}">← Prev</a>
    {{else}}
      <span class="page-btn disabled">← Prev</span>
    {{end}}
    {{range .PageNumbers}}
    {{if eq . 0}}
      <span class="page-ellipsis">…</span>
    {{else if eq . $.Page}}
      <span class="page-btn active">{{.}}</span>
    {{else}}
      <a class="page-btn" href="?sort={{$.Sort.Field}}&order={{$.Sort.Order}}&page={{.}}">{{.}}</a>
    {{end}}
    {{end}}
    {{if .HasNext}}
      <a class="page-btn" href="?sort={{.Sort.Field}}&order={{.Sort.Order}}&page={{.Page | plus1}}">Next →</a>
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
    <table class="files-table dirs-table">
      <thead>
        <tr>
          <th class="col-name">Name</th>
          <th class="col-date">Modified</th>
        </tr>
      </thead>
      <tbody>
        {{range .Subdirs}}
        <tr>
          <td class="col-name"><a class="table-link" href="{{.Href}}"><span class="chip-icon">📁</span>{{.Name}}/</a></td>
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
    <table class="files-table others-table">
      <thead>
        <tr>
          <th class="col-name">Name</th>
          <th class="col-type">Type</th>
          <th class="col-size">Size</th>
          <th class="col-date">Modified</th>
        </tr>
      </thead>
      <tbody>
        {{range .OtherFiles}}
        <tr>
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

  {{if gt .TotalImages 0}}
  <section class="images-section">
    <h2 class="section-heading">Media</h2>
    <div class="image-grid">
      {{range .Images}}
      <a class="card{{if .IsVideo}} video{{end}}" href="{{.Href}}">
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
          <span class="open-btn" data-open-url="{{.Href}}" role="button" tabindex="0" title="Open in new tab" aria-label="Open in new tab">↗</span>
        </div>
        <div class="tile-name">{{.Name}}</div>
        <div class="tile-meta">
          <div class="tile-meta-info">
            <span class="date">{{.Date}}</span>
            <span class="size">{{.Size}}</span>
          </div>
          <span class="filetype-chip">{{.Type}}</span>
        </div>
      </a>
      {{end}}
    </div>

    {{if gt .TotalPages 1}}
    <nav class="pagination">
      {{if .HasPrev}}
        <a class="page-btn" href="?sort={{.Sort.Field}}&order={{.Sort.Order}}&page={{.Page | minus1}}">← Prev</a>
      {{else}}
        <span class="page-btn disabled">← Prev</span>
      {{end}}
      {{range .PageNumbers}}
      {{if eq . 0}}
        <span class="page-ellipsis">…</span>
      {{else if eq . $.Page}}
        <span class="page-btn active">{{.}}</span>
      {{else}}
        <a class="page-btn" href="?sort={{$.Sort.Field}}&order={{$.Sort.Order}}&page={{.}}">{{.}}</a>
      {{end}}
      {{end}}
      {{if .HasNext}}
        <a class="page-btn" href="?sort={{.Sort.Field}}&order={{.Sort.Order}}&page={{.Page | plus1}}">Next →</a>
      {{else}}
        <span class="page-btn disabled">Next →</span>
      {{end}}
    </nav>
    {{end}}
  </section>
  {{else}}
  <p class="empty">No images in this directory.</p>
  {{end}}
</main>
<footer class="site-footer">
  proudly served by <a href="https://caddyserver.com" rel="noopener" target="_blank">caddy</a> + <a href="https://github.com/synapticloop/caddy_image_gallery" rel="noopener" target="_blank">synapticloop // image gallery</a>
</footer>
<script>

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
    '<span class="lb-caption"></span>';
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
      var headings = document.querySelectorAll('.dirs-section .section-heading, .others-section .section-heading');
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
	if _, statErr := os.Stat(tmplPath); statErr == nil {
		return template.New(clean).Funcs(galleryFuncs).ParseFiles(tmplPath)
	}
	// Fall back to the bundled constant.
	return template.New("gallery").Funcs(galleryFuncs).Parse(galleryTemplate)
}

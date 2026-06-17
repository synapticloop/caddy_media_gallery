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
	Page        int // 1-based
	PageSize    int
	TotalImages int
	TotalPages  int
	HasPrev     bool
	HasNext     bool

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
	Type     string // uppercase extension without dot, e.g. "JPG", "DIR", "MP4"
	Size     string // human-readable, e.g. "234 KB" — for dirs this is empty
	Date     string // ISO date "2006-01-02" — for dirs this is empty
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
// `src` (no thumb generation). The field is still called ThumbURL
// (for template compatibility) but its value is the original file
// path in this case.
func buildFileView(f FileInfo, pathPrefix, thumbPrefix string, noThumbs bool) FileView {
	v := FileView{
		Name: f.Name,
		Type: formatType(f.Name, f.Kind == KindDir),
	}
	switch f.Kind {
	case KindDir:
		v.IsDir = true
		v.Href = pathPrefix + f.Name + "/"
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
	return
}

// sortFiles sorts a slice of FileInfo by the given spec. The
// slice is sorted in place. Sort field "mtime" is the natural
// scan order (already sorted by the scanner); we honour it by
// NOT re-sorting (the scanner's order is the most recent first).
func sortFiles(files []FileInfo, spec SortSpec) {
	if spec.Field == "mtime" || spec.Field == "" {
		return // scanner already sorted
	}
	asc := spec.Order == "asc"
	switch spec.Field {
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
	case "date":
		sort.SliceStable(files, func(i, j int) bool {
			if asc {
				return files[i].ModTime < files[j].ModTime
			}
			return files[i].ModTime > files[j].ModTime
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
func RenderPage(title, pathPrefix, thumbPrefix, relPath, tmplName string, noThumbs bool, pageSize int, files []FileInfo, query url.Values) (string, error) {
	sortSpec := parseSort(query)
	page := pageFromQuery(query)

	dirs, others, allImages := splitFiles(files)
	sortFiles(allImages, sortSpec)
	if pageSize <= 0 {
		pageSize = 50
	}
	paged := paginate(allImages, page, pageSize)

	totalImages := len(allImages)
	totalPages := (totalImages + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}

	// Split dirs into Up (the synthetic ../ entry, only present
	// in subdirs) and Subdirs (the actual subdirs). The Up
	// entry is rendered on its own line (always first when
	// present); Subdirs is rendered in a tight row with no
	// gap between chips, per the user's 2026-06-17 spec.
	subdirViews := buildFileViews(dirs, pathPrefix, thumbPrefix, noThumbs)
	var up *FileView
	if relPath != "" {
		up = &FileView{
			Name:  "Up",
			Href:  "../",
			IsDir: true,
			IsUp:  true,
		}
	}

	data := PageData{
		Title:       title,
		PathPrefix:  pathPrefix,
		ThumbPrefix: thumbPrefix,
		Up:          up,
		Subdirs:     subdirViews,
		OtherFiles:  buildFileViews(others, pathPrefix, thumbPrefix, noThumbs),
		Images:      buildFileViews(paged, pathPrefix, thumbPrefix, noThumbs),
		Page:        page,
		PageSize:    pageSize,
		TotalImages: totalImages,
		TotalPages:  totalPages,
		HasPrev:     page > 1,
		HasNext:     page < totalPages,
		Sort:        sortSpec,
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
func buildFileViews(files []FileInfo, pathPrefix, thumbPrefix string, noThumbs bool) []FileView {
	out := make([]FileView, 0, len(files))
	for _, f := range files {
		out = append(out, buildFileView(f, pathPrefix, thumbPrefix, noThumbs))
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
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<meta name="color-scheme" content="light">
<title>{{.Title}}</title>
<style>

* { box-sizing: border-box; margin: 0; padding: 0; }
html, body { background: #f3f6f7; color: #333; }
body {
  font-family: Inter, -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, system-ui, sans-serif;
  font-size: 16px;
  line-height: 1.5;
  text-rendering: optimizeLegibility;
  min-height: 100vh;
  padding: 2rem 1rem 4rem;
}
a { color: #006ed3; text-decoration: none; }
a:hover { color: #0095e4; }
main {
  max-width: 1200px;
  margin: 0 auto;
  background: white;
  border-radius: 5px;
  box-shadow: 0 2px 5px 1px rgba(0, 0, 0, 0.05);
  overflow: hidden;
}
header {
  padding: 1.25rem 2rem 1rem;
  border-bottom: 1px solid #e5e9ea;
}
.header-top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 1rem;
  margin-bottom: 0.85rem;
}
.header-main { flex: 1 1 auto; min-width: 0; }
h1 {
  font-size: 1.5rem;
  font-weight: 600;
  color: #111;
  margin-bottom: 0.25rem;
}
.meta {
  font-size: 0.875rem;
  color: #666;
  display: flex;
  gap: 0.5rem;
  flex-wrap: wrap;
}
.meta span { color: #888; }
.sort-indicator {
  flex: 0 0 auto;
  align-self: flex-start;
  margin-top: 0.3rem;
  font-size: 0.8rem;
  padding: 0.35rem 0.75rem;
  background: white;
  border: 1px solid #e5e9ea;
  border-radius: 4px;
  color: #333;
  text-decoration: none;
  white-space: nowrap;
  transition: background 0.12s, border-color 0.12s;
}
a.sort-indicator:hover { background: #f3f6f7; border-color: #d0d4d6; color: #006ed3; }
.sort-indicator .arrow { margin-left: 0.3rem; font-weight: 600; }
.section {
  padding: 1.25rem 2rem;
  border-bottom: 1px solid #e5e9ea;
}
.section:last-child { border-bottom: none; }
.section-heading {
  font-size: 0.7rem;
  font-weight: 700;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: #888;
  margin-bottom: 0.75rem;
}
.chip-row {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
/* Dirs section layout (Phase 24, per user request 2026-06-17):
   - Up chip is rendered on its OWN LINE, always first
   - Subdirs are rendered in a TIGHT row with NO GAP between
     chips (the user said "remove the spacing for the rest of
     the directories - it doesn't look as I want")
   - The dirs section is only shown if there's an Up entry
     OR at least one subdir */
.dirs-section .up-chip-row {
  margin-bottom: 0.5rem; /* visual separation from the subdirs below */
}
.dirs-section .dirs-row {
  display: flex;
  flex-wrap: wrap;
  gap: 0;                /* no spacing between subdir chips */
  align-items: center;
}
.dirs-section .dirs-row .chip {
  /* Slightly tighten the visual appearance of subdirs (no gap
     + a smaller border-radius) so they read as a single
     cluster rather than independent chips. */
  border-radius: 3px;
  padding: 0.35rem 0.7rem;
}
.dirs-section .dirs-row .chip:not(:last-child) {
  border-right-width: 0; /* prevent double borders between adjacent chips */
}
.chip {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.4rem 0.75rem;
  background: #f3f6f7;
  border: 1px solid #e5e9ea;
  border-radius: 4px;
  color: #333;
  font-size: 0.8rem;
  text-decoration: none;
  white-space: nowrap;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  transition: background 0.12s, border-color 0.12s;
}
.chip:hover { background: #e5e9ea; border-color: #d0d4d6; color: #006ed3; }
.chip-icon { font-size: 0.95rem; line-height: 1; }
.dir-chip { font-weight: 500; }
.images-section { padding: 1.25rem 2rem 1.5rem; }
.dirs-section, .others-section { padding: 1rem 2rem; }
.sort-bar {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  font-size: 0.85rem;
  flex-wrap: wrap;
  padding-top: 0.75rem;
  border-top: 1px solid #e5e9ea;
}
.sort-label { color: #888; margin-right: 0.25rem; }
.sort-btn {
  display: inline-flex;
  align-items: center;
  padding: 0.3rem 0.65rem;
  border: 1px solid #e5e9ea;
  border-radius: 4px;
  color: #333;
  text-decoration: none;
  background: white;
  transition: background 0.12s, border-color 0.12s;
}
.sort-btn:hover { background: #f3f6f7; border-color: #d0d4d6; }
.sort-btn.active {
  background: #006ed3;
  border-color: #006ed3;
  color: white;
  font-weight: 500;
}
.sort-btn.active:hover { background: #0095e4; border-color: #0095e4; }
.sort-btn .arrow { margin-left: 0.2rem; font-weight: 600; }
.image-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: 1rem;
}
.card {
  display: flex;
  flex-direction: column;
  background: white;
  border: 1px solid #e5e9ea;
  border-radius: 5px;
  overflow: hidden;
  text-decoration: none;
  color: inherit;
  transition: border-color 0.12s, transform 0.12s;
}
.card:hover { border-color: #006ed3; transform: translateY(-1px); }
.thumb {
  position: relative;
  width: 100%;
  aspect-ratio: 1 / 1;
  background: #f3f6f7;
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
  color: #333;
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
  transform: scale(1.1);
  background: #fff;
}
.tile-name {
  font-size: 0.8rem;
  font-weight: 500;
  color: #222;
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
  color: #888;
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
  background: #e5e9ea;
  color: #333;
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
  margin-top: 1.5rem;
  font-size: 0.85rem;
}
.page-btn {
  display: inline-block;
  padding: 0.4rem 0.75rem;
  border: 1px solid #e5e9ea;
  border-radius: 4px;
  color: #333;
  text-decoration: none;
  background: white;
}
.page-btn:hover { background: #f3f6f7; border-color: #d0d4d6; }
.page-btn.disabled {
  color: #bbb;
  background: #fafbfc;
  cursor: not-allowed;
  pointer-events: none;
}
.page-info {
  padding: 0 0.75rem;
  color: #666;
}
.empty {
  padding: 2rem;
  text-align: center;
  color: #888;
}
@media (max-width: 600px) {
  body { padding: 1rem 0.5rem 3rem; }
  header, .dirs-section, .others-section, .images-section { padding-left: 1rem; padding-right: 1rem; }
  .image-grid { grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); }
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
#gallery-lightbox img {
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
#gallery-lightbox .lb-btn:hover { color: #4dabff; }
#gallery-lightbox .lb-close { top: 1rem; right: 1.5rem; }
#gallery-lightbox .lb-prev { left: 1.5rem; top: 50%; transform: translateY(-50%); }
#gallery-lightbox .lb-next { right: 1.5rem; top: 50%; transform: translateY(-50%); }
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
          <span>{{.TotalImages}} images</span>
          {{if gt (len .OtherFiles) 0}}<span>·</span><span>{{len .OtherFiles}} other files</span>{{end}}
          {{if or .Up (gt (len .Subdirs) 0)}}<span>·</span><span>{{if .Up}}{{len .Subdirs}} {{else}}{{len .Subdirs}}{{end}} directories</span>{{end}}
          <span>·</span><span>{{.PageSize}} per page</span>
        </div>
      </div>
      {{if eq .Sort.Field "mtime"}}
      <span class="sort-indicator" title="Default sort: most recently modified first">Sort: {{sortLabel .Sort.Field}}<span class="arrow">{{if eq .Sort.Order "asc"}} ↑{{else}} ↓{{end}}</span></span>
      {{else}}
      <a class="sort-indicator" href="?" title="Reset to default sort (most recently modified first)">Sort: {{sortLabel .Sort.Field}}<span class="arrow">{{if eq .Sort.Order "asc"}} ↑{{else}} ↓{{end}}</span></a>
      {{end}}
    </div>
    <div class="sort-bar">
      <span class="sort-label">Sort by</span>
      <a class="sort-btn{{if eq .Sort.Field "name"}} active{{end}}" href="?sort=name&order={{if and (eq .Sort.Field "name") (eq .Sort.Order "asc")}}desc{{else}}asc{{end}}">Name<span class="arrow">{{if eq .Sort.Field "name"}}{{if eq .Sort.Order "asc"}} ↑{{else}} ↓{{end}}{{end}}</span></a>
      <a class="sort-btn{{if eq .Sort.Field "type"}} active{{end}}" href="?sort=type&order={{if and (eq .Sort.Field "type") (eq .Sort.Order "asc")}}desc{{else}}asc{{end}}">Type<span class="arrow">{{if eq .Sort.Field "type"}}{{if eq .Sort.Order "asc"}} ↑{{else}} ↓{{end}}{{end}}</span></a>
      <a class="sort-btn{{if eq .Sort.Field "mtime"}} active{{end}}" href="?sort=mtime&order={{if and (eq .Sort.Field "mtime") (eq .Sort.Order "asc")}}desc{{else}}asc{{end}}">Modified<span class="arrow">{{if eq .Sort.Field "mtime"}}{{if eq .Sort.Order "asc"}} ↑{{else}} ↓{{end}}{{end}}</span></a>
      <a class="sort-btn{{if eq .Sort.Field "size"}} active{{end}}" href="?sort=size&order={{if and (eq .Sort.Field "size") (eq .Sort.Order "asc")}}desc{{else}}asc{{end}}">Size<span class="arrow">{{if eq .Sort.Field "size"}}{{if eq .Sort.Order "asc"}} ↑{{else}} ↓{{end}}{{end}}</span></a>
    </div>
  </header>

  {{if or .Up (gt (len .Subdirs) 0)}}
  <section class="dirs-section">
    <h2 class="section-heading">Directories</h2>
    {{if .Up}}
    <div class="up-chip-row">
      <a class="chip dir-chip up-chip" href="{{.Up.Href}}"><span class="chip-icon">↑</span> <span class="chip-icon">📁</span> Up (../)</a>
    </div>
    {{end}}
    {{if .Subdirs}}
    <div class="dirs-row">
      {{range .Subdirs}}
      <a class="chip dir-chip" href="{{.Href}}"><span class="chip-icon">📁</span>{{.Name}}/</a>
      {{end}}
    </div>
    {{end}}
  </section>
  {{end}}

  {{if .OtherFiles}}
  <section class="others-section">
    <h2 class="section-heading">Other files</h2>
    <div class="chip-row">
      {{range .OtherFiles}}
      <a class="chip" href="{{.Href}}"><span class="chip-icon">📄</span>{{.Name}}</a>
      {{end}}
    </div>
  </section>
  {{end}}

  {{if gt .TotalImages 0}}
  <section class="images-section">
    <h2 class="section-heading">Images</h2>
    <div class="image-grid">
      {{range .Images}}
      <a class="card{{if .IsVideo}} video{{end}}" href="{{.Href}}">
        <div class="thumb{{if .IsVideo}} thumb-video{{end}}">
          {{if .IsVideo}}
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
      <span class="page-info">Page {{.Page}} of {{.TotalPages}}</span>
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
<script>

(function() {
  var overlay = document.createElement('div');
  overlay.id = 'gallery-lightbox';
  overlay.innerHTML =
    '<button class="lb-btn lb-close" aria-label="Close">×</button>' +
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

  // Only image cards (have an <img> child). Videos and files are
  // skipped — they keep their default link behavior.
  var cards = Array.prototype.slice.call(
    document.querySelectorAll('.card')
  ).filter(function(c) { return c.querySelector('img'); });
  var idx = 0;

  function clear() {
    if (currentEl) { currentEl.remove(); currentEl = null; }
  }

  function show(i) {
    if (cards.length === 0) return;
    idx = ((i % cards.length) + cards.length) % cards.length;
    var c = cards[idx];
    var href = c.getAttribute('href') || '';
    var name = (c.querySelector('.tile-name') || {}).textContent || '';
    clear();
    var img = document.createElement('img');
    img.src = href;
    img.alt = name;
    currentEl = img;
    media.appendChild(img);
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
  overlay.querySelector('.lb-prev').addEventListener('click', function() { show(idx - 1); });
  overlay.querySelector('.lb-next').addEventListener('click', function() { show(idx + 1); });
  media.addEventListener('click', function(e) { e.stopPropagation(); show(idx + 1); });
  document.addEventListener('keydown', function(e) {
    if (!overlay.classList.contains('open')) return;
    if (e.key === 'Escape') close();
    else if (e.key === 'ArrowLeft') show(idx - 1);
    else if (e.key === 'ArrowRight') show(idx + 1);
  });
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

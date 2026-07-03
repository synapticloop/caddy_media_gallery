# Templates: structure and variables

## How the template loading works

```
loadTemplate(name string) reads $GALLERY_TEMPLATES_DIR
   │
   ├── name = gallery.Template (from Caddyfile `template` directive)
   │   default "gallery.tmpl" if the directive is absent
   │
   ├── sanitizeTemplateName(name):
   │   reject absolute paths, reject ".." (any traversal)
   │
   ├── /etc/caddy/gallery-templates/<clean name> exists?
   │     ├── YES to parse that file directly (CSS+JS are inside it)
   │     │         (the on-disk file is the active template)
   │     └── NO  to use the bundled galleryTemplate constant
   │
   └── return *template.Template ready to RenderPage
```

The `template` Caddyfile directive is the operator-facing knob
that picks the template file. See
[`docs/configuration.md`](01-configuration.md) for the directive
syntax and the path-traversal protection details.

**Note on `no_thumbs`:** the template is unaffected by the
`no_thumbs` Caddyfile directive. The template always uses
`{{.ThumbURL}}` for the tile `<img src>`; the `no_thumbs` flag
changes the *value* of that field (to the original file URL
when true, to the thumb URL when false), not the field name or
its usage. So a template that works with thumbs also works with
no_thumbs (and vice versa), with no template changes needed.

The template is a single self-contained file. The HTML, CSS
(inside `<style>`), and JS (inside `<script>`) all live in one
Go string constant (`galleryTemplate` in `render.go`) and one
on-disk file (`/etc/caddy/gallery-templates/gallery.tmpl`).
There is no sub-template loading — the previous Phase 16 design
that split them into 3 files (`gallery.tmpl` + `style.css` +
`lightbox.js`) was collapsed in Phase 17 for easier editing.

The bundled constant is the source of truth at build time. The
on-disk file is an *override* layer; if you delete it, the
module falls back to the bundled version automatically on the
next request.

## What's in the single template file

One file, `gallery.tmpl`, ~16.7 KB / 574 lines, containing:

| Section | What's in it | Approx lines |
|---|---|---|
| `<!DOCTYPE>` ... `<style>` | Document head, including the full CSS inlined as a `<style>` block | ~10 + 365 (CSS) |
| `<body>`, `<main>`, `<header>` | Title, counts, sort indicator, sort bar | ~50 |
| Directories section | `{{if .Directories}}` ... `{{end}}` — the dirs chip strip | ~7 |
| Other files section | `{{if .OtherFiles}}` ... `{{end}}` — the others chip strip | ~7 |
| Images section | Sort info, paginated grid, per-tile HTML | ~80 |
| Pagination | `{{if gt .TotalPages 1}}` ... `{{end}}` — prev/next links | ~15 |
| `<script>` ... `</script>` | The full JS inlined (lightbox, open-in-new-tab, sort indicator) | ~100 (JS) |

The CSS rules and the HTML they apply to are interleaved
top-to-bottom in the file, so when you scroll through the
template you see the structure (HTML), the styling (CSS), and
the behavior (JS) in document order. Easier to hand-edit than
three separate files.

## Template variables

The template receives a `PageData` struct. All fields you can
reference from `{{...}}` are listed below.

### Top-level fields (on `PageData`)

| Field | Type | What it's for |
|---|---|---|
| `{{.Title}}` | `string` | The page title (rendered in `<title>` and the `<h1>`). Currently the directory name. |
| `{{.PathPrefix}}` | `string` | Prefix for relative links to files in the same directory. Usually `"./"`. |
| `{{.ThumbPrefix}}` | `string` | Prefix for thumbnail URLs. Usually `"./_thumbs/"`. |
| `{{.Up}}` | `*FileView` | The synthetic "Up" entry for the parent directory. nil at the gallery root. |
| `{{.Subdirs}}` | `[]FileView` | Subdirectory entries. Always rendered in full (not paginated), case-insensitive alphabetical. |
| `{{.OtherFiles}}` | `[]FileView` | Non-media files (HTML, txt, etc.). Always rendered in full (not paginated). |
| `{{.Images}}` | `[]FileView` | The image + video tiles for the **current page only**. Paginated, sorted per the user's `?sort=&order=` choice. |
| `{{.Page}}` | `int` | Current page number (1-based). |
| `{{.PageSize}}` | `int` | Current per-page count (driven by the dropdown / `?page_size=` query). |
| `{{.PageSizes}}` | `[]string` | The visitor's per-page dropdown options (e.g. `["30", "60", "120", "all"]`). |
| `{{.ImageStart}}` | `int` | 1-based start of the current page's image range (e.g. 1 for page 1, 61 for page 2). |
| `{{.ImageEnd}}` | `int` | 1-based end of the current page's image range (e.g. 60 for page 1 of 60-per-page, 89 for page 2 of 60-per-page of a 89-image dir). |
| `{{.TotalImages}}` | `int` | Total images in the directory (across all pages, after type/q filters). |
| `{{.TotalPages}}` | `int` | Total page count. |
| `{{.TotalFiles}}` | `int` | Total files in the directory (images + videos + other files). |
| `{{.TotalVideos}}` | `int` | Total video files in the directory. |
| `{{.ImageCount}}` | `int` | Count of image files only (videos excluded). |
| `{{.TotalAllFilesSize}}` | `string` | Human-readable total size of all files. |
| `{{.HasPrev}}` | `bool` | True if `{{.Page}} > 1`. |
| `{{.HasNext}}` | `bool` | True if `{{.Page}} < {{.TotalPages}}`. |
| `{{.PageNumbers}}` | `[]int` | Google-style page-number list for the pagination nav. May contain 0 (a "..." ellipsis). |
| `{{.Sort}}` | `SortSpec` | The current sort. See below. |
| `{{.SearchQuery}}` | `string` | The raw `?q=` value (preserved across sort changes). |
| `{{.SearchMatch}}` | `string` | The operator-configured search match mode (`"word"` or `"substring"`). Used to render a `data-search-match` attribute on the search input so the inline JS uses the same rule as the server. |
| `{{.TypeFilter}}` | `map[string]bool` | The active type filter (e.g. `{"jpg": true, "png": true}` for `?type=jpg,png`). |
| `{{.IsTypeFilterActive}}` | `bool` | True if any type filter is active. |
| `{{.TypeFilterQuery}}` | `string` | The raw `?type=` value (the canonical, comma-separated form). |
| `{{.FilterImageOptions}}` / `{{.FilterVideoOptions}}` / `{{.FilterOtherOptions}}` | structs | Per-type filter groups (each option's extension, label, count, selected). |
| `{{.Breadcrumb}}` | `[]BreadcrumbSegment` | Breadcrumb trail from gallery root to current dir. Each segment has `Name`, `Href`, `IsCurrent`. |
| `{{.Query}}` | `url.Values` | Raw URL query — useful for the per-page form to preserve all params except the ones it overrides. |

### `{{.Sort}}` (a `SortSpec` struct)

| Field | Type | What it's for |
|---|---|---|
| `{{.Sort.Field}}` | `string` | One of `"mtime"`, `"name"`, `"type"`, `"size"`. (The `?sort=` URL param. `"mtime"` is the default.) |
| `{{.Sort.Order}}` | `string` | `"asc"` or `"desc"`. (The `?order=` URL param.) |

### Per-entry fields (on `FileView`)

When you `{{range .Images}}` (or `.Directories` / `.OtherFiles`),
each iteration gives you a `FileView` with these fields:

| Field | Type | What it's for |
|---|---|---|
| `{{.Name}}` | `string` | The basename (`"photo.jpg"`, `"subdir"`, etc.). Truncated with ellipsis in the live template if too long for the tile. |
| `{{.Href}}` | `string` | Relative link to the file. Use as `<a href="{{.Href}}">`. |
| `{{.ThumbURL}}` | `string` | For images and videos, the relative thumbnail URL (e.g. `./_thumbs/photo.webp`). **Empty string for non-media files** — check `{{if .ThumbURL}}` before using. |
| `{{.IsDir}}` | `bool` | True for directories. |
| `{{.IsImage}}` | `bool` | True for image files. |
| `{{.IsVideo}}` | `bool` | True for video files. Videos go in the image grid with a play-button overlay (no `<img>` child); `{{.ThumbURL}}` is set but the live template doesn't render an `<img>` for them. |
| `{{.IsOther}}` | `bool` | True for non-media files (HTML, txt, etc.). |
| `{{.Type}}` | `string` | Uppercase extension without the dot: `"JPG"`, `"DIR"`, `"MP4"`, `"HTML"`, etc. |
| `{{.Size}}` | `string` | Human-readable file size: `"234 KB"`, `"1.2 MB"`, etc. **Empty string for directories.** |
| `{{.Date}}` | `string` | ISO date `"2006-01-02"` (UTC-normalised). **Empty string for directories.** |

### Template functions (the funcmap)

The template engine has a few helper functions registered:

| Func | Signature | What it does |
|---|---|---|
| `minus1` | `minus1 n int to int` | Returns `n - 1`. Used for prev-page link targets. |
| `plus1` | `plus1 n int to int` | Returns `n + 1`. Used for next-page link targets. |
| `lastIndex` | `lastIndex s []T to int` | Returns `len(s) - 1`. Used for the breadcrumb. |
| `sortLabel` | `sortLabel field string to string` | Maps a sort field code to its display label: `"name"to"Name"`, `"type"to"Type"`, `"mtime"to"Modified"`, `"size"to"Size"`, `"date"to"Date"`. Unknown fields fall back to the raw field name capitalised. Empty string to `"Modified"` (the default). |
| `queryString` | `queryString q url.Values to template.URL` | Renders `url.Values` as a `key=val&key=val` query string. Used for building pagination / sort / filter links. Returns empty string if no params. |
| `queryWith` | `queryWith q url.Values, key, value string to url.Values` | Returns a new `url.Values` with the given key replaced (or removed if value is empty). |
| `queryForPage` | `queryForPage q url.Values, sort SortSpec, page int to url.Values` | Pagination-specific: keeps the effective sort/order, replaces `page`. |
| `sortURL` | `sortURL q url.Values, field, order string to url.Values` | Sort-toggle-specific: sets `sort` + `order`, resets `page` to 1. |
| `sortOrder` | `sortOrder currentField, field, currentOrder string to string` | Returns the toggled order (asc → desc if same field, else the new field's default). |
| `queryToHiddenInputs` | `queryToHiddenInputs q url.Values to template.HTML` | Renders `url.Values` as hidden `<input>` elements. Used by the per-page form to preserve other params on submit. |
| `queryToHiddenInputsExclude` | `queryToHiddenInputsExclude q url.Values, exclude ...string to template.HTML` | Like above, but with a variadic list of keys to exclude. Used by the per-page form to omit `page` (so changing size resets to page 1) and `page_size` (the dropdown supplies it). |
| `formatDimensions` | `formatDimensions w, h int to string` | Returns `"WIDTH × HEIGHT"` (e.g. `"1024 × 1024"`) or empty if either is 0. |

# Templates

The gallery is rendered by an `html/template` (Go's standard
template engine). The template is bundled in the binary as a
Go string constant (`galleryTemplate` in `render.go`), and on
each Caddy startup the module also writes it to disk at
`/etc/caddy/gallery-templates/gallery.tmpl` (or
`$GALLERY_TEMPLATES_DIR` if set). The on-disk file exists so
operators can read the template without opening the binary, and
so they can override individual templates by editing the file
in place.

## How the template loading works

```
loadTemplate() reads $GALLERY_TEMPLATES_DIR
   │
   ├── /etc/caddy/gallery-templates/gallery.tmpl exists?
   │     ├── YES → parse that file, append the bundled style.css + lightbox.js
   │     │         (the on-disk gallery.tmpl is the active template)
   │     └── NO  → use the bundled galleryTemplate constant
   │               (the bundled style.css + lightbox.js are also constants)
   │
   └── return *template.Template ready to RenderPage
```

The bundled constants are the source of truth at build time. The
on-disk file is an *override* layer; if you delete it, the module
falls back to the bundled version automatically on the next
request.

## What the bundled templates are

Three files in `$GALLERY_TEMPLATES_DIR`:

| File | Source constant in render.go | Purpose |
|---|---|---|
| `gallery.tmpl` | `galleryTemplate` (~107 lines) | The main HTML. The `<style>` and `<script>` blocks are inlined as named sub-templates (`{{template "style.css" .}}` etc.). |
| `style.css` | `styleCSS` (~365 lines) | The stylesheet, inlined in `<style>` in the page `<head>`. Pure CSS, no preprocessing. |
| `lightbox.js` | `lightboxJS` (~99 lines) | The vanilla-JS click-to-expand overlay. Adapts to whatever tiles are on the current page (knows about `.card` with an `img` child). |

`style.css` and `lightbox.js` are not loaded as separate files
on the page — they're parsed as named sub-templates of the main
`gallery.tmpl` template, so a single HTTP response carries all
three. The on-disk files exist as a discoverability/override
mechanism, not as separate page assets.

## Template variables

The template receives a `PageData` struct. All fields you can
reference from `{{...}}` are listed below.

### Top-level fields (on `PageData`)

| Field | Type | What it's for |
|---|---|---|
| `{{.Title}}` | `string` | The page title (rendered in `<title>` and the `<h1>`). Currently the directory name. |
| `{{.PathPrefix}}` | `string` | Prefix for relative links to files in the same directory. Usually `"./"`. |
| `{{.ThumbPrefix}}` | `string` | Prefix for thumbnail URLs. Usually `"./_thumbs/"`. |
| `{{.Directories}}` | `[]FileView` | Subdirectory entries. Always rendered in full (not paginated), case-insensitive alphabetical. |
| `{{.OtherFiles}}` | `[]FileView` | Non-media files (HTML, txt, etc.). Always rendered in full (not paginated). |
| `{{.Images}}` | `[]FileView` | The image + video tiles for the **current page only**. Paginated, sorted per the user's `?sort=&order=` choice. |
| `{{.Page}}` | `int` | Current page number (1-based). |
| `{{.PageSize}}` | `int` | Images per page (constant; default 50). |
| `{{.TotalImages}}` | `int` | Total images in the directory (across all pages). |
| `{{.TotalPages}}` | `int` | Total page count. |
| `{{.HasPrev}}` | `bool` | True if `{{.Page}} > 1`. |
| `{{.HasNext}}` | `bool` | True if `{{.Page}} < {{.TotalPages}}`. |
| `{{.Sort}}` | `SortSpec` | The current sort. See below. |

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
| `minus1` | `minus1 n int → int` | Returns `n - 1`. Used for prev-page link targets. |
| `plus1` | `plus1 n int → int` | Returns `n + 1`. Used for next-page link targets. |
| `sortLabel` | `sortLabel field string → string` | Maps a sort field code to its display label: `"name"→"Name"`, `"type"→"Type"`, `"mtime"→"Modified"`, `"size"→"Size"`, `"date"→"Date"`. Unknown fields fall back to the raw field name capitalised. Empty string → `"Modified"` (the default). |

## Editing the templates — the basics

1. **Find the templates dir.** On this host it's
   `/etc/caddy/gallery-templates/`. The file is `gallery.tmpl`.
2. **Edit the file in place.** The module re-reads it on every
   request, so changes take effect immediately — no Caddy restart
   needed.
3. **Test in a browser.** The next request will use the new
   template. If the template has a parse error, the module will
   return 500 with the parser error in the response body. Fix
   the syntax and try again.
4. **Revert to the bundled version.** If you want to go back to
   what the binary ships with, delete the on-disk file:
   `sudo rm /etc/caddy/gallery-templates/gallery.tmpl`. The next
   request falls back to the bundled constant.

## Walkthrough: change the header to dark mode

The bundled template is light-themed. To make it dark:

1. Edit `/etc/caddy/gallery-templates/gallery.tmpl`.
2. Find the `<style>` block. It's the `{{template "style.css" .}}`
   reference. The actual CSS is loaded as a sub-template — you
   can't edit it via the `gallery.tmpl` file directly.
3. To override the CSS, write a new `style.css` file at
   `/etc/caddy/gallery-templates/style.css`. The module's
   `loadTemplate` always parses the bundled `styleCSS` as the
   `style.css` named sub-template after parsing the on-disk
   `gallery.tmpl`, so the on-disk `style.css` would need
   `loadTemplate` to load it instead. **As of this writing, the
   on-disk override of `style.css` and `lightbox.js` is
   supported as files-on-disk, but `loadTemplate` always uses
   the bundled constants for those two.** The on-disk files are
   present for operator visibility, not for serving.

   **Practical workaround:** copy the `styleCSS` constant from
   `render.go` into `/etc/caddy/gallery-templates/style.css`,
   edit the colors there, then patch `render.go` to read the
   on-disk `style.css` instead of the constant. (This is a
   code-level change, not a template-only change.)

   **The intended way** to do dark mode in v1 is to fork the
   project and edit the `styleCSS` constant in `render.go`. The
   planned v2 (per the wiki page) is to have `loadTemplate` also
   read on-disk `style.css` and `lightbox.js` as overrides.

4. Save the file. The next request renders the new style.

## Walkthrough: add a "Created" column to the image tiles

If you want to show the file's created time alongside the modified
date (the template currently shows `Date` which is the modified
date):

1. Note: `FileView.Date` is the only date field exposed. The
   `FileInfo` struct in the scanner *does* have `ModTime`
   (in nanoseconds) but no `CreatedTime`. Adding a "Created"
   column would require a code change — extend `FileView` with a
   `Created string` field, populate it in `RenderPage` from
   `info.ModTime` (or from `os.Stat` birthtime via
   `syscall.Statx` on Linux), then reference it in the
   template as `{{.Created}}`.

   In other words: this is a code change, not a template-only
   change. The template is fully driven by the Go struct fields
   — anything you want to display has to be in the struct.

## Where the bundled templates are defined in source

If you want to read the source (or fork and customise):

| File | Constant | Lines (approx) |
|---|---|---|
| `render.go` | `galleryTemplate` | line 392, ~107 lines |
| `render.go` | `styleCSS` | line 504, ~365 lines |
| `render.go` | `lightboxJS` | line 874, ~99 lines |

All three are Go string literals (`const foo = \`...\``) in the
`gallery` package. To customise: edit the constant, rebuild the
module (`./build.sh`), restart Caddy (`sudo systemctl restart
caddy`). The on-disk templates are written from the new
constants on the next startup.

## Troubleshooting

**Edit took effect but the page looks the same.** Hard-reload
(Cmd-Shift-R / Ctrl-F5). The browser may have cached the
previous HTML.

**Edit took effect but the page is a 500.** Your template has a
parse error. The response body will contain the Go
`html/template` parser error. Common causes:
- Unclosed `{{if}}` / `{{range}}` / `{{with}}` blocks
- `{{end}}` mismatch (most common — Go templates require `{{end}}` to close every block)
- Calling a method that doesn't exist on the data type (e.g. `{{.Foo.Bar}}` when `Foo` is a string)

**Edit took effect but layout is broken.** You removed or
re-ordered a structural element. The CSS selectors and the JS
`querySelector` calls expect a specific DOM shape — see
`styleCSS` and `lightboxJS` for the contract.

**Want to test a new template before deploying it.** Stage the
edit in a new file at, say, `/etc/caddy/gallery-templates/gallery.tmpl.staging`,
then copy it over the live file once you're happy. Or
`curl` the gallery to see the live HTML and check it with a
browser inspector before saving.

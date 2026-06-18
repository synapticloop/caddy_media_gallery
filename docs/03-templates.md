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
| `minus1` | `minus1 n int to int` | Returns `n - 1`. Used for prev-page link targets. |
| `plus1` | `plus1 n int to int` | Returns `n + 1`. Used for next-page link targets. |
| `sortLabel` | `sortLabel field string to string` | Maps a sort field code to its display label: `"name"to"Name"`, `"type"to"Type"`, `"mtime"to"Modified"`, `"size"to"Size"`, `"date"to"Date"`. Unknown fields fall back to the raw field name capitalised. Empty string to `"Modified"` (the default). |

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

## Walkthrough: change the theme to dark mode

Since the CSS is now inlined in the same file as the HTML, you
edit the `<style>` block directly in `gallery.tmpl` — no second
file to coordinate, no sub-template indirection.

1. Open `/etc/caddy/gallery-templates/gallery.tmpl` in your
   editor.
2. Find the `<style>` block. It's the big CSS block near the
   top of the file, just after `<title>...</title>`. Search for
   `html, body { background: #f3f6f7;` to find the body color.
3. Change the colors inline. The CSS is plain CSS — no
   preprocessing. Most of the theme colors are concentrated in
   the first 50-100 lines.
4. Save the file. The change takes effect on the next request
   (no Caddy restart needed — the module re-reads the on-disk
   template on every `loadTemplate` call).
5. Hard-reload in the browser to bypass the HTML cache.

A common set of swaps for dark mode (find/replace these in the
`<style>` block):

| Light (default) | Dark variant |
|---|---|
| `html, body { background: #f3f6f7; }` | `html, body { background: #1a1a1a; color: #ddd; }` |
| `main { background: white; }` | `main { background: #222; }` |
| `header { border-bottom: 1px solid #e5e9ea; }` | `header { border-bottom: 1px solid #333; }` |
| `.chip { background: #f3f6f7; border: 1px solid #e5e9ea; }` | `.chip { background: #2a2a2a; border: 1px solid #3a3a3a; }` |
| `a { color: #006ed3; }` | `a { color: #4ea3ff; }` |

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
| `render.go` | `galleryTemplate` | line 392, ~574 lines (HTML + inlined CSS + inlined JS) |

A single constant. CSS and JS are inlined inside `<style>` and
`<script>` blocks respectively. To customise: edit the constant,
rebuild the module (`./build.sh`), restart Caddy (`sudo systemctl
restart caddy`). The on-disk template is written from the new
constant on the next startup.

## Upgrading from a pre-inlining install (Phase 16 to Phase 17)

If your site was running the old 3-file template split
(`gallery.tmpl` + `style.css` + `lightbox.js`) and you upgraded
to the new inlining build, the on-disk files are in an
inconsistent state:

- `style.css` and `lightbox.js` from the old build are now
  dead weight. `writeBundledTemplates` removes them
  automatically on the next Provision after upgrade. Safe to
  ignore.
- The on-disk `gallery.tmpl` from the old build still has
  the old `{{template "lightbox.js" .}}` references, which
  no longer work. The new `loadTemplate` will fail to parse
  this file and the gallery will 500.

**One-time fix on upgrade:**

```
sudo rm /etc/caddy/gallery-templates/gallery.tmpl
sudo systemctl restart caddy
```

The next Provision writes the new inlined template. After that,
the on-disk file is the canonical inlined version, and any
operator edits to it become live overrides.

## Upgrading the bundled template content

`writeBundledTemplates()` deliberately does NOT overwrite an
existing `gallery.tmpl` on disk — that's the operator-override
contract. When the bundled template's contents change (e.g.
new CSS rule, new template branch, fixed layout bug), the
on-disk file is NOT updated automatically.

**To pick up the new bundled content:**

```bash
# 1. Save any local customisations (if you have any)
diff /etc/caddy/gallery-templates/gallery.tmpl      /home/osmanj/projects/caddy_image_gallery/render.go
# 2. Delete the on-disk file
sudo rm /etc/caddy/gallery-templates/gallery.tmpl
# 3. Restart Caddy so the next Provision runs writeBundledTemplates
sudo systemctl restart caddy
# 4. (Optional) Re-apply your local customisations
```

This workflow is intentional: operators who have customised the
template (e.g. themed dark mode) keep their changes across
upgrades. Operators who haven't customised just get the
"fresh bundled version" after the rm + restart.

**Future enhancement (not v1):** writeBundledTemplates could
write a content hash into a sidecar file, and on Provision, if
the sidecar hash doesn't match the bundled hash, overwrite the
on-disk file. This would auto-update without breaking the
operator-override contract (the operator could still delete the
on-disk file to fall back to the bundled version, OR write a
sidecar with a custom hash to pin to their version).

## Troubleshooting

**Edit took effect but the page looks the same.** Hard-reload
(Cmd-Shift-R / Ctrl-F5). The browser may have cached the
previous HTML.

**Edit took effect but the page is a 500.** Your template has
a parse error. The response body will contain the Go
`html/template` parser error. Common causes:
- Unclosed `{{if}}` / `{{range}}` / `{{with}}` blocks
- `{{end}}` mismatch (most common — Go templates require `{{end}}` to close every block)
- Calling a method that doesn't exist on the data type (e.g. `{{.Foo.Bar}}` when `Foo` is a string)
- An unescaped backtick inside a Go template comparison (backticks terminate the Go raw string the constant is in)

**Edit took effect but layout is broken.** You removed or
re-ordered a structural element. The CSS selectors and the JS
`querySelector` calls expect a specific DOM shape — search
for the class name in the template to see what's expected.

**Want to test a new template before deploying it.** Stage the
edit in a new file at, say, `/etc/caddy/gallery-templates/gallery.tmpl.staging`,
then copy it over the live file once you're happy. Or
`curl` the gallery to see the live HTML and check it with a
browser inspector before saving.

**Got a 500 immediately after upgrading.** You have the
pre-inlining on-disk `gallery.tmpl` from a previous build.
See the "Upgrading from a pre-inlining install" section above.


## Dark mode + theme toggle

The template supports three themes:

1. **Auto** (default) — follows the visitor's OS preference via the
   `prefers-color-scheme` CSS media query. macOS, Windows, iOS,
   and Android all expose this preference; if the visitor's OS is
   in dark mode, the gallery renders in dark mode automatically.
2. **Light** — forces light mode regardless of OS preference.
3. **Dark** — forces dark mode regardless of OS preference.

The choice is presented as a small toggle button group in the page
header (top-right area), next to the sort UI. Three buttons:
Three buttons: a gear icon (Auto), a sun icon (Light), a moon icon (Dark). The current state is marked
with `aria-pressed="true"` and a slightly inset background.

The choice persists across visits via `localStorage` under the key
`gallery-theme`. Values stored: `auto` | `light` | `dark`. `auto`
is the default (no attribute set on `<html>`); `light` and `dark`
set the `data-theme` attribute on `<html>`.

### No flash of wrong theme

There's a tiny inline `<script>` in the `<head>` that reads
`localStorage` and applies the `data-theme` attribute to `<html>`
BEFORE the body paints. This runs synchronously during HTML parsing,
so the CSS already sees the right theme when the body renders. No
flash of light theme when the visitor has chosen dark.

### How dark mode is implemented

All colors are defined as CSS custom properties (CSS variables) in
the `:root` selector at the top of the template's `<style>` block.
There are ~15 tokens: `--bg`, `--bg-card`, `--bg-chip`, `--bg-hover`,
`--bg-active`, `--fg`, `--fg-muted`, `--fg-faint`, `--fg-disabled`,
`--border`, `--border-strong`, `--accent`, `--accent-hover`, `--shadow`,
`--shadow-strong`.

The dark mode override is just a second block of token assignments
that applies when:

- `@media (prefers-color-scheme: dark) { :root:not([data-theme="light"]) { ... } }`
  — the OS is in dark mode AND the user hasn't explicitly picked
  light. The `:not([data-theme="light"])` selector is what makes
  the "Auto" state work: when the OS is dark but the user picked
  Light, the dark tokens don't apply.
- `[data-theme="dark"] { ... }` — manual dark mode regardless of
  OS preference. Triggered by clicking the moon icon; choice
  persists in localStorage.

### Customizing colors

To override a color, edit the on-disk template at
`/etc/caddy/gallery-templates/gallery.tmpl` and change the token
value in the `:root` block. For example, to make the accent color
green instead of blue:

```css
:root {
  --accent: #2e8b57;       /* was #006ed3 */
  --accent-hover: #3aa86a; /* was #0095e4 */
}
```

To add a new dark-mode-specific color, define it in all three
blocks (`:root`, the media query, and `[data-theme="dark"]`).

### Lightbox (always dark)

The lightbox overlay (the full-screen image/video viewer) has its
own dark colors that are theme-independent — the dark background
and white controls work in both modes. This is intentional: a dark
overlay focuses attention on the content regardless of the page
theme.

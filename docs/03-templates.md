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

The choice is presented as a small toggle button group in the
page header. Three buttons: a gear icon (Auto), a sun icon (Light),
a moon icon (Dark). The current state is marked
with `aria-pressed="true"` and a slightly inset background.

**Position (Phase 50/51):** the toggle was originally at the
top-LEFT of the header (just to the left of the page title). As
of Phase 50, it sits at the top-RIGHT, where the old sort-order
indicator used to be. The sort indicator itself was removed in
Phase 50 (the sort UI still exists as the sort-bar below the
header — the active sort field is still visible there).

**Mobile layout (Phase 50/51):** on screens ≤600px wide, the
header stacks vertically via `@media (max-width: 600px) {
.header-top { flex-direction: column; }`. The visual order on
mobile is:

1. **Theme toggle** (top, right-aligned via `align-self: flex-end`)
2. **h1 + meta line** (below, full width)

This is achieved with CSS `order` on the flex children, not
by reordering the HTML source (the source order stays h1 →
meta → toggle, which is better for screen readers):

```css
.header-main { order: 2; }
.theme-toggle { order: 1; align-self: flex-end; }
```

The toggle's row order (`order: 1`) puts it above the header
content (`order: 2`) on mobile; the `align-self: flex-end` keeps
it right-aligned. On desktop (the default), neither `order` nor
`flex-direction` applies, so the layout reverts to the normal
horizontal flex with the toggle at the right (via the
`justify-content: space-between` on `.header-top`).

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
There are ~16 tokens, divided into semantic groups:

| Group | Tokens |
|---|---|
| Backgrounds | `--bg` (page), `--bg-card` (cards), `--bg-chip` (chips), `--bg-hover` (chip hover), `--bg-active` (active chip) |
| Text | `--fg` (primary), `--fg-muted` (secondary), `--fg-faint` (tertiary), `--fg-disabled` (disabled) |
| Borders & shadows | `--border`, `--border-strong`, `--shadow`, `--shadow-strong` |
| Accents | `--accent` (links + borders), `--accent-hover`, `--accent-bg` (button fills — separate from `--accent` so the dark-mode button can be a muted darker blue without dimming link text) |

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

**Why `--accent` and `--accent-bg` are separate tokens:** the
accent color serves two visual roles — text/borders (good as a
bright color on a dark bg, e.g. `#4dabff`) and button fills
(looks glaring as a bright color, should be muted, e.g. `#3b6fb6`).
Splitting them lets each role have its own dark-mode value.

### Dark mode refinements (Phase 40–42)

The first dark-mode pass had two issues:

- **Chip bg too light** (Phase 40): the chips (directories, other
  files, sort buttons, page buttons) had a `--bg-chip` value
  (`#1d1d1d` in dark mode) that was visibly lighter than the page
  bg (`#1a1a1a`), making the chips stand out as bright blobs. Fixed
  by setting `--bg-chip: #1a1a1a` (same as `--bg`) in dark mode, so
  chips blend in with the page; only the border + text show. This
  mirrors the light-mode behavior (where `--bg-chip` already equals
  `--bg`).
- **Hardcoded colors in 11 CSS rules** (Phase 41): a regex-based
  refactor (the original dark-mode implementation) caught 18 rules
  but missed 11 more that used hardcoded light-mode hex values.
  After a comprehensive sweep, all CSS rules now use `var()` token
  references. The only `#hex` colors left in the on-disk template
  are: the token definitions themselves (light + dark overrides),
  the video tile placeholder gradient (theme-independent), and the
  play button colors (white-on-dark, theme-independent).

### Customizing colors

To override a color, edit the on-disk template at
`/etc/caddy/gallery-templates/gallery.tmpl` and change the token
value in the `:root` block. For example, to make the accent color
green instead of blue:

```css
:root {
  --accent: #2e8b57;       /* was #006ed3 */
  --accent-hover: #3aa86a; /* was #0095e4 */
  --accent-bg: #2e8b57;    /* was #006ed3 — used by active sort/page buttons */
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

## Header meta line format

The page header (below the page title) shows a meta line summarizing
the directory's contents. The format (Phase 44/45/55) is:

```
34 images · 8 videos · 2 other files // (8.3 MB total) // 4 directories · 50 per page · Page 1 of 2
└─────┬─────┘ └────┬────┘ └─────┬───────┘ └──────┬───────┘  └───┬─────┘ └──┬─────┘ └──┬───────┘
      count       count       count            total size     count     page size page indicator
                                              (ALL files:                              (when
                                              images + videos                          TotalPages
                                              + other files)                           > 1)
```

Note the format after the `//` size segment: the directory count
uses a single **space** separator (not `·`), then the rest of the
items use `·`. This was changed in Phase 55 — the size is
visually marked as "special" by the `//` framing, and the regular
meta items after it use a single character (`·` or space)
consistently.

**Meta items, in order:**

1. **Image count** — `N images`. Always shown.
2. **Video count** — `· N videos`. Shown only when `N > 0`.
3. **Other files count** — `· N other files`. Shown only when `N > 0`.
4. **Total size** — `// (X.X KB total) //`. Wrapped in `//`
   separators (visually distinct from the `·` separator used for
   the file counts). Represents the SUM of `Size` for all files in
   the directory: images + videos + other files. Excludes
   subdirectories. The literal word `total` follows the size
   inside the parens, making the meaning clear. Pre-formatted via
   the `humanSize()` helper (B / KB / MB / GB). After the closing
   `//`, the next meta item is separated by a single space
   (not `·`).
5. **Directory count** — `N directories` (single space separator
   after `//`, then the count). Shown when the user is in a
   subdir (N = subdirs of the current dir) or when there are
   subdirs in the root listing.
6. **Per page size** — `· 50 per page`. Always shown.
7. **Page indicator** — `· Page X of Y`. Shown only when
   `TotalPages > 1` (multi-page gallery).

**Why the size uses `//` separators instead of `·`:** the `//`
visually distinguishes the size from the other meta items (which
all use `·`). The size is conceptually different — it's a
quantity (bytes), not a count. The `//` is a "this is special"
marker.

**Implementation:** the meta line is rendered by the template at
the top of the `<style>` block's wrapping `<header>`. The size
segment uses three separate `<span>` elements (one for each `//`,
one for the parens) so the browser's flex `gap: 0.5rem` adds
visual spacing between them.


## Open-in-new-tab button

The template has a small **↗** button on each image/video tile
(top-right corner of the card) that opens the file's URL in a
new browser tab. Added in Phase 47; refined in Phase 52.

**Visual:**
- Position: `top: 6px, right: 6px` inside the `.card`
- Size: 28×28 px
- Background: `rgba(255, 255, 255, 0.85)` — light translucent
- Dark border: `border: 2px solid #000` (Phase 47) so the button
  stands out more
- Arrow color: `#111111` (Phase 52) — a fixed dark color,
  **NOT** the theme-aware `--fg` token
- The character itself is `north east arrow` (U+2197 NORTH EAST ARROW, displayed as the arrow glyph in the bundled template)
- Default opacity: `0.5` (subtle). On hover/focus, opacity goes
  to `1.0` and the button scales up slightly.

**Why the arrow color is fixed `#111111` (not `var(--fg)`):**
the button has a light translucent background
(`rgba(255,255,255,0.85)`) that stays light over ANY page
background (light or dark). A dark arrow on a light button
background is always visible — no need to adapt to the page's
theme. The `--fg` token stays at its current values
(`#111111` light / `#e5e5e5` dark) and continues to be used by
other elements (h1, body, meta, sort buttons, etc.) — only
`.open-btn` is excluded from the theme-aware color rule.

**Behavior:**
- Click or Enter on the button → `window.open(href, '_blank',
  'noopener,noreferrer')` opens the file in a new tab
- The `noopener,noreferrer` flags are a security best practice
  (prevents the new tab from accessing `window.opener`)
- The click event is `stopPropagation`'d so it doesn't also
  trigger the parent's "open in lightbox" handler

## Lightbox controls

The lightbox overlay has 4 buttons + 2 text labels:

| Element | Position | Behavior |
|---|---|---|
| `.lb-close` (×) | Top-right (inside `.lb-controls` pill) | Closes the lightbox; Esc key |
| `.lb-open` (↗) | Top-right (inside `.lb-controls` pill, left of close) | Opens the current image/video in a new tab |
| `.lb-prev` (‹) | Full-height hit area on the **left** side (120px wide) | Previous image; Left-arrow key |
| `.lb-next` (›) | Full-height hit area on the **right** side (120px wide) | Next image; Right-arrow key |
| `.lb-counter` | Bottom-left (or bottom-center on mobile) | "N / total" text |
| `.lb-caption` | Bottom-center | The current file's name |

**The `.lb-controls` rounded pill (Phase 48):** the open (↗)
and close (×) buttons are wrapped in a single flex container
`.lb-controls` so they appear as a single rounded pill at the
top-right of the lightbox. The container has:
- Position: `top: 1rem, right: 1.5rem`
- Background: `rgba(255, 255, 255, 0.92)` (light pill on the
  dark lightbox)
- Border: `2px solid #000`
- Border-radius: `10px` (the "rounded box")
- Padding: `4px` around the buttons
- Display: `flex` (lays out open + close side-by-side, perfectly
  aligned vertically and horizontally)

The two buttons inside have:
- `position: static` (flex lays them out, not absolute)
- Transparent background (the container provides it)
- No individual border
- 28×28 px
- Dark icon color (`#1a1a26`)
- `border-radius: 6px` on each button (rounded corners within
  the pill)
- Hover: subtle dark background tint (`rgba(0, 0, 0, 0.08)`)

**Why a pill instead of separate buttons:** a single rounded
container gives the two related actions (close + open) a visual
unity — they're both "exit the lightbox" actions (close or
view-on-its-own), and grouping them makes that clearer.

**The prev/next buttons are NOT in the pill** — they're at the
left/right edges of the lightbox. These are navigation actions
(different from exit actions), so they get their own positioning.

**Prev/next hit areas (Phase 65):** the prev/next buttons are
**full-window-height × 120px wide** hit areas positioned at
`left: 0` / `right: 0`. The arrow icon is flex-centered inside
the hit area. At rest, the hit area is **transparent** (no
visible button) — only a hover reveals the target.

- **Hover background — theme-aware:**
  - Dark mode (default; the page bg is dark): the hover bg is
    `rgba(255, 255, 255, 0.08)` — a subtle whiter tint over the
    dark lightbox. The user sees a soft "highlight" where they're
    pointing.
  - Light mode (page bg is light): the hover bg is
    `rgba(0, 0, 0, 0.06)` — a subtle darker tint. The lightbox
    itself is still theme-independent (always dark), but the
    hover tint adapts so it works for visitors on light pages.

**Why a full-height hit area:**
- On touch devices (mobile, tablets), the user doesn't have a
  precise cursor — a small button in the middle of the screen
  is hard to hit. A full-height strip on each side gives a
  large, easy target.
- On desktop, the same hit area means the user can click
  anywhere in the left or right strip — no need to aim.

**Why 120px wide:** enough to be a comfortable target on touch
screens (~7mm at typical DPI), small enough to leave the center
~60% of the screen clear for the image/video content.

**Why transparent at rest:** the lightbox is about the content.
A visible button on each side would distract from the image.
The hover reveals the hit area, so the user sees it when they're
navigating, not when they're viewing.

**JS — `stopPropagation` (Phase 65 fix):** the new hit areas sit
on top of the media (z-index: 1). The media element has its own
click handler that advances to the next image (for `<img>`).
Without `stopPropagation` on the prev/next click handlers, a
single click in the prev/next area would advance TWICE — once
from the button, once from the bubbling media click. The fix:
both prev/next handlers call `e.stopPropagation()` before
calling `show(idx±1)`.

**Why an alpha-blended hover (rgba) instead of a solid color:**
the lightbox shows whatever media is loaded. A solid bg color
would clash with some images (e.g., a green hover on a photo
of a red rose). An alpha-blended fill mixes with whatever's
behind, giving a consistent "tint" effect that works on any
media.

**Why a dark border:** like the tile `.open-btn`, the pill has
a black 2px border so it stands out clearly against the dark
lightbox background. The light pill on a dark bg with a dark
border is a strong visual "this is a control" signal.

**Keyboard navigation:**
- `Escape` → close
- `Left arrow` → previous image
- `Right arrow` → next image
- (Clicking on the image itself also goes to next, like
  carousel UIs)

The `data-theme` attribute on `<html>` is NOT read by the
lightbox CSS — the lightbox is intentionally theme-independent
(dark always, with light controls). This is the same design
choice as the lightbox bg and counter/caption: focus
on the content, regardless of page theme.

**Video poster (Phase 63):** when a video tile has a generated
thumbnail (Phase 62's ffmpeg pipeline), the lightbox video
element sets its HTML5 `poster` attribute to the thumb URL
(extracted from the same `<img class="thumb-img">` element
on the tile). The browser shows the poster image immediately
when the video opens in the lightbox — the user sees the
video's first frame as a still image, then on click the video
swaps to playback. This is the same mechanism YouTube uses
to show a thumbnail before a video plays.

If `no_video_thumbs` is set OR ffmpeg is missing, the tile
has no `<img class="thumb-img">` so the JS can't find a
poster URL — the `poster` attribute is simply not set, and
the browser shows its default (black frame, or the first
decoded frame if `preload="metadata"` is enabled).

The poster URL points at the same cached WebP that's used
for the tile thumbnail, so no extra server work or storage
is required.


## Pagination

The pagination nav is rendered in **two** places (Phase 54):

1. **Top** — after the sort-bar (Name/Type/Modified/Size buttons), before the DIRECTORIES section
2. **Bottom** — after the IMAGES grid (existing position)

Both pagination navs are identical — same buttons (`Prev`,
page numbers, `Next`), same styling, same conditional (only
shown when `TotalPages > 1`). Only the position differs.

**Why mirror:**

- Visitors on a long page don't have to scroll back to the top
  to switch pages — they can click `Next` at the top.
- The pagination at the top also serves as a "you are here"
  indicator when arriving on a non-first page (so the user knows
  they're not on page 1).
- Symmetry: the sort-bar is at the top, the pagination at the
  bottom; having pagination at the top mirrors the bottom.

The same `{{if gt .TotalPages 1}}` conditional guards both
instances, so single-page galleries don't show any pagination
at all.

**Pagination item format:** uses the same Google-style pattern
introduced in Phase 29 — `First` (when far from start),
`Prev`, page numbers with ellipsis in the middle, `Next`,
`Last` (when far from end). The `aria-pressed` is set on the
current page number for accessibility.

## Footer

At the bottom of every gallery page, below the IMAGES grid (and
below the bottom pagination if multi-page), a small footer credits
the underlying technologies (Phases 56/58/59):

```
─────────────────────────────────────────
           proudly served by caddy + synapticloop // image gallery
```

- **"caddy"** — links to https://caddyserver.com (the web server)
- **"synapticloop // image gallery"** — links to
  https://github.com/synapticloop/caddy_image_gallery (this
  plugin's repo)

Both links have `rel="noopener" target="_blank"` (security best
practice for new-tab links).

**Styling:** centered, small text (`0.8rem`), muted color
(`var(--fg-muted)`), subtle padding. No border-top (removed in
Phase 58 — the muted color provides enough visual distinction).

**Why these credits:**
- "caddy" — Caddy is the web server. Without it, this plugin
  wouldn't exist; credit the underlying tech.
- "synapticloop // image gallery" — links to this plugin's repo
  (not just the synapticloop org page) so visitors can read the
  source, file issues, or fork the project.

The footer text is operator-visible but not configurable — if
you want to change it, edit the template (`<footer class="site-footer">`
in the bundled template). Operators who fork the project can
brand the footer however they like.

## Section heading: "MEDIA" (not "IMAGES")

The gallery renders both **images** AND **videos** (since Phase 25
when the lightbox got video support). To reflect the broader scope,
the IMAGES section heading was renamed to **MEDIA** in Phase 60.

In the bundled template:

```html
<h2 class="section-heading">Media</h2>
```

If you fork the template and want to revert to "IMAGES" (e.g., you
have a strictly-photo gallery with no videos), change this line.
The CSS (`.section-heading`) is unchanged — only the visible text.

The other section headings remain "Directories" and "Other files"
(unaffected by Phase 60).

## Mobile: video play button fix (Phase 61)

On mobile devices, clicking the play button on a video in the
lightbox used to advance to the next media file instead of
starting playback. Root cause: the media click handler always ran
`show(idx + 1)` regardless of media type. On mobile, tapping the
video's native play button fires a click event on the `<video>`
element, and the handler advanced to the next file BEFORE the
video could play. Fix (Phase 61): check
`currentEl.tagName === 'VIDEO'` in the handler and bail out so
the browser's native click handling (play/pause) takes over.

For images, click-to-advance still works (unchanged). For
videos, click is no longer hijacked — the user can tap the play
button on mobile to start playback, or click elsewhere on the
video (e.g. the time bar / volume) for the native controls to
handle. Navigation is via the prev/next buttons or arrow keys.

## Open-in-new-tab button (Phase 47+52)

The tile-level open-in-new-tab button (the small ↗ in the
top-right of each tile) is documented separately in its own
section above. Recent refinements:

- **Phase 47** — added the dark border (`border: 2px solid #000`)
  so the button stands out more. The light translucent bg
  (`rgba(255,255,255,0.85)`) stays light over both light and
  dark page bgs, so a dark border is always visible.
- **Phase 52** — arrow color fixed at `#111111` (was
  `var(--fg)`). The user explicitly wanted `--fg` UNCHANGED
  (still `#111111` light / `#e5e5e5` dark) and used by other
  elements. The open-btn arrow stays dark in both modes because
  the button's bg is always light.

## Building the PDF locally

The PDF book (`caddy-image-gallery-book.pdf`) is built from the
markdown files in `docs/` via pandoc + xelatex. The full command
is wrapped in a script at the project root: `build-docs.sh`.

**To rebuild the PDF locally:**

```bash
./build-docs.sh
```

The script:
- Works from any directory (uses `BASH_SOURCE` to find its
  own location and the project root)
- Verifies prerequisites (`pandoc`, `xelatex`) before running
- Verifies all source files exist before running
- Runs pandoc with the right flags (`--pdf-engine=xelatex`,
  `--include-in-header=preamble.tex`, `--listings`)
- Cleans up intermediate files (`*.aux`, `*.log`, etc.) after
- Prints summary info (output path, file size, page count)

**Prerequisites:**
- `pandoc`
- `xelatex` (usually in the `texlive-xetex` package)
- Install on Debian/Ubuntu:
  `sudo apt-get install pandoc texlive-xetex texlive-fonts-recommended`
- Install on macOS:
  `brew install pandoc mactex`

**Why a script instead of running pandoc directly:**
the pandoc command has several flags (`--include-in-header=preamble.tex`
references the font config; `--listings` is needed for code block
formatting; the source file order matters). The script encapsulates
all of this so you don't have to remember the incantation.

The font config (`preamble.tex` + `docs/fonts/`) references absolute
paths for `fontspec`'s `Path =` option. If you move this project
to another machine, update the `Path =` line in `preamble.tex`
to match the new location.

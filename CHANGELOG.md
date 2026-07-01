# Changelog

All notable changes to `caddy_media_gallery` are documented here. Dates are in
YYYY-MM-DD format. Commit hashes are short (7 chars).

The project started as `caddy_image_gallery` and was renamed to `caddy_media_gallery`
on 2026-06-19 to better reflect that it serves images, videos, and other files
(not just images).

---

## 2026-07-01

### 🐛 Port change: local-install default is now 3245 (was 8080)

`build.sh --user` now defaults to port **3245** (= 0xCAD in hex — a small easter egg for the project's homepage, since C-A-D happen to all be valid hex digits and the abbreviation is memorable). The script's comments, the auto-generated `Caddyfile.user`, and the help text (`build.sh --help`) all reflect the new default.

To keep using 8080 (the prior default), pass `--user 8080` or set `CADDY_USER_PORT=8080`. All operator documentation (README.md, docs/00-readme.md, docs/01-configuration.md) updated to reflect the new default; remaining 8080 references in docs are intentional ("to override, use 8080...").

---

## 2026-06-30

### UI / Button styling
- `2c3fae5` refactor: rename `Apply` button to `Filter`
- `2154e97` refactor: move `All` button after `Apply`, rename to `Reset`
- `23b18ca` fix: `All` pill and `Reset` button now use the same color scheme as Sort by buttons
- `493f51b` fix: `Search all` is black (primary), `Reset` matches `All` pill (secondary)
- `26c9dc9` fix: `Apply` button now same height as `Search all` / `Reset` (1px font diff)

### Template refactor
- `dad72f3` refactor: move gallery template to separate file with `//go:embed`
  - Template moved from embedded Go string in `render.go` to `templates/gallery.tmpl`
  - New `template_embedded.go` file with `//go:embed templates/gallery.tmpl`
  - Runtime override behavior preserved (on-disk file at `/etc/caddy/gallery-templates/gallery.tmpl` still takes precedence)

### Search header
- `8400fa9` feat: MEDIA header format — `'X of Y'` for form submit, `'X of Y THIS PAGE'` for JS search
- `4f0aa24` feat: keep `'Media (DIRECTORY_TOTAL -'` prefix when search is active
- `464c1b7` feat: filter-form preserves `page_size` on search submit
- `041a625` fix: search header updates to JS format when user changes text after form submit
- `681e315` feat: include search phrase in MEDIA header (e.g. `"search 'st' -"`)
- `4c5a6ce` fix: search header default is no-search format (not form-submitted text)
- `4872f5a` refactor: search filter now greys out non-matching items instead of hiding

### Directories header
- `f731f9c` feat: directories header shows `"+1 parent"` when there's a parent
- `e4703f7` refactor: directory header now shows `'+ Parent Directory'` in italics

### Pagination
- `dbbf6e1` fix: pagination links no longer turn blue on hover

### Mobile
- `d5ba7c7` fix: wrap type filter elements in a div so they stay grouped on mobile

### Hover tooltip
- `e375bc5` feat: hover tooltip on thumbnails shows filename (no ext, no `_` or `-`)
  - Native browser tooltip (`title` attribute) + custom CSS tooltip (`:before` pseudo-element)
  - Filename transformation: strip extension, replace `_` and `-` with spaces
  - Example: `misty_bamboo_forest_path.jpg` → `"misty bamboo forest path"`

### EXIF sidecar (stale detection + format)
- `1eb7c8a` fix: detect stale `.meta` and `.exif` sidecars via source mtime check
- `d36ded2` refactor: EXIF sidecar keys use Human-Readable names (matching lightbox labels)
- `6f6b9f6` refactor: EXIF sidecar uses plain text format (no JSON) for speed

### Other filter — `(none)` entry
- `e029117` feat: include `(none)` entry in Other dropdown for files without extensions
  - Files like `Makefile` or `welcome` (no extension) now appear in the Other dropdown
  - Two bugs fixed: directories without extension no longer counted as files; files with no extension no longer silently skipped
- `df15d41` fix: `(none)` is a strict filter — only show files without an extension
  - Sentinel value `.` (literal dot) in the form (can't be a real file extension)
  - `parseTypeFilter` translates `.` to `""` in the filter map
  - `applyTypeFilter` checks `filter[""]` for the strict no-extension filter
  - Multi-select OR logic: `?ext=.&ext=.md` shows files matching either

### Documentation
- `439e937` docs: add CHANGELOG.md with all commits grouped by date and category
- `47a30b4` docs: refresh screenshots to show EXIF pill on strawberry (after `no_exif` removed from localhost bypass)
- `3249e2c` docs: update README with new features (EXIF pill, hover tooltip, + Parent Directory, sidecars, //go:embed)

---

## 2026-06-29

- `2764360` feat: `no_exif` Caddyfile directive to disable EXIF reading entirely
  - Skips EXIF parse at scan time, endpoint returns 404
- `ac69c8b` fix: sort bar links preserve the page parameter (instead of resetting to page 1)
- `baaab59` feat: breadcrumb + dirs-table links preserve all query params (`q`, `type`, `sort`, `order`, `page_size`) but reset page to 1

---

## 2026-06-28

### Search
- `22cd797` fix: JS overwrites correct server-rendered search header
- `88d86c4` fix: search header `N` value (per user clarification)
- `991c934` feat: search header format with `"search showing M of N <em>This page</em>"`
- `1054774` feat: search-aware media section header (server-side + JS)
- `84f4e73` feat: add `search_match` Caddyfile config (`word`|`substring`, default `substring`)

### Page size
- `912f4b4` fix: per-page dropdown now shows `"all"` as selected when `?page_size=all`
- `f163927` fix: `"all"` option in per-page dropdown now shows all items
- `2f832bd` fix: exclude `page_size` from the page-size form's hidden inputs
- `02af751` fix: changing page size always resets to page 1

### Cache / performance
- `9abab25` feat: cache stats footer — `XX // YY // ZZ // AA` in hex
- `7cd8709` feat: add `max_cache_size_mb` Caddyfile directive (default 1 GB, `0` = unbounded)
- `b061782` feat: subtle shimmer animation while thumbnails are loading

### Buttons
- `f7f2361` style: sort button hover now matches the Search all button
- `1635d4f` fix: `Apply` + `Reset` button hover states keep text contrast
- `f678489` feat: search `Reset` button next to `"Search all"`

### Breadcrumbs
- `0fca13f` revert: remove the `"/"` breadcrumb separators
- `6adad1e` style: breadcrumb `"/"` separators are darker and bigger
- `51bf05e` feat: large `"/"` separators in breadcrumbs (between each segment + at the start)

### Documentation
- `eaf67d3` docs: bring all documentation up to date with current feature set
- `85bfdc4` feat: media header shows total + current page range

---

## 2026-06-27

- `70d6eff` style: move dimensions watermark from bottom-right (card) to bottom-left (image)
- `f44b81b` feat: source image/video dimensions watermark on thumbnails
- `731e049` feat: EXIF metadata display in lightbox + EXIF pill on card
- `ad73418` fix: filter dropdowns no longer auto-open
- `af3b5cb` style: remove background from `.sort-indicator` Block 1
- `e3c3727` style: remove coloring from active sort indicators and headers
- `88590b2` style: remove border, border-radius, padding from `.sort-indicator` Block 1
- `63c2870` style: `.sort-indicator` — remove border + padding, add margin-top
- `b1b86be` fix: add table IDs so the header-sort JS can find the tables
- `97def0c` fix: pagination + sort-bar links preserve all URL query params
- `e15a352` style: label the per-page dropdown `"Show [N] Per page"`
- `236064f` feat: clickable column headers with persistent sort (URL + localStorage)
- `2418fe6` feat: dirs table size column now shows sum of file sizes in subdir
- `73a5761` style: rename `"# Dirs"` to `"# Sub-Dirs"` with non-breaking spaces
- `7817235` fix: directory listing always shows, even when a filter is active
- `33a48e1` feat: dirs table now has `# Items`, `# Dirs`, and `Size` columns
- `d8d2cbe` fix: `?page_size=N` URL parameter is now honoured
- `0123511` refactor: rename `num_per_page` back to `page_size` + default 60
- `e9e6428` fix: default page size is the operator's declared first item
- `41952c2` refactor: rename `page_sizes` → `num_per_page` + sorted dropdown
- `aea2b31` style: rename `"Filter"` label to `"Type Filter"`

---

## 2026-06-26

- `2443b46` feat: search interface (client-side + server-side, word-boundary match)
- `e97779a` style: remove `padding: 4px 0 0 0` from `.section-toggle`
- `e04d46c` fix: breadcrumb root name now resolves correctly in Provision
- `7ba3c83` style: add `margin-top: -0.25rem` to `.page-size-select`
- `134b762` style: page-size dropdown matches filter-pill look + preserves URL params
- `a0857c5` fix: page-size dropdown template type mismatch + test fixes
- `c8c8e5f` feat: configurable `page_sizes` dropdown + default 60
- `5bc8638` fix: add missing `root_name` case to `UnmarshalCaddyfile`
- `1093802` fix: add `border-bottom` back to `.breadcrumb-link` + collapse to one line
- `142895c` fix: remove duplicate `.breadcrumb-link` block with `border-bottom`
- `0522c01` refactor: remove `»` separator + drop `border-bottom` on breadcrumb links
- `fc69323` feat: `root_name` Caddyfile directive + fix breadcrumb bottom border
- `9583ec8` refactor: `»` separator moved inside the breadcrumb link
- `1f76b09` refactor: rectangular breadcrumb with `»` separator + fix `/images/` display bug
- `6c6185a` fix: chevron duplicate + overlap + current chevron colour
- `745c347` refactor: breadcrumb order + chevron style (filters below breadcrumbs)
- `216da1b` fix: breadcrumb links are absolute URLs when `path_prefix` is set
- `b084d29` refactor: `Apply` button uses `--active-*` color scheme (matches sort/pagination)
- `988e936` fix: media section toggle JS now picks up `.media-section`
- `d92cbd2` refactor: filter above breadcrumb, less left padding, fix breadcrumb order
- `24dbee1` feat: add show/hide toggle to Media section (with the line)
- `82e7b3a` feat: filter UI with dropdowns + Apply button (Phase 4)
- `96c5251` feat: server-rendered breadcrumb (Phase 3)
- `b4f2296` feat: server-side `?type=` filter plumbing (Phase 2)
- `c358cdf` refactor: rename `images-section`/`image-grid` to `media-*`, make heading-divider more visible
- `004f93f` feat: configurable `image_types` and `video_types` via Caddyfile

---

## 2026-06-25

### Documentation / build
- `fae8150` docs: add SIL OFL 1.1 font credits page before the endplate
- `ae08f38` docs: document that ffmpeg detection is startup-only; log the resolved path
- `3d54300` docs: add local install (no sudo) section to 3 operator docs
- `ed365f0` feat: local install (no sudo) via `build.sh --user [PORT]`
- `e17748b` docs: add tagline `"The delightful way to serve a directory."`
- `b5171d3` feat: cache parsed template across requests (Phase 102)
- `d21ae3c` docs: refresh the PDF + use the new cover image + portability fixes
- `b3ae85d` docs: add new docs to README Documentation section
- `fa1dbee` docs: Updated the README file
- `25ffd17` docs: add the 3 source PNG screenshots to git (dark, light, lightbox)
- `5a5310d` docs: add lightbox screenshot to README + explanation
- `8f3ca69` docs: add animated preview GIF to README + update title text

### Animated fade GIF
- `2ea5cc6` feat: hold first and last frame for 3 seconds each in the fade GIF
- `db235bc` feat: add animated fade GIF (light → dark) for the docs screenshots

### Lightbox
- `aae668a` refactor: remove lightbox text labels (revert Phase 86 + 88)
- `80d40de` refactor: remove `align-items: center` from `.section-toggle`
- `09e7dc0` fix: add `padding-left` to the sort-by arrow (↑/↓)
- `edab437` feat: lightbox button labels enclosed in same grey rounded bg as the icon
- `6dce9b2` feat: lightbox buttons have rotated text labels (Open in new tab, Close)
- `f88baf7` feat: active sort + pagination buttons invert page colors (not blue)
- `e1c3d0a` feat: bigger lightbox close icon (✕ instead of ×)

---

## 2026-06-24

### Project rename: `caddy_image_gallery` → `caddy_media_gallery`
- `3fe7af0` refactor: rename project to `caddy_media_gallery` (was `caddy_image_gallery`)
- Module path changed, all references updated (Caddyfile directives, file paths, docs)

### Tables
- `8be8db1` fix: up-row-table now has `font-size: 0.85rem` (was inheriting 1rem)
- `2358459` refactor: up-row-table td no longer has `font-weight: 500`
- `30f2f59` refactor: `.files-table .col-type` width `auto` (was 6rem)
- `0ba01f9` refactor: replace `.sort-bar` negative-margin hack with `.header-top` border + padding
- `041849e` feat: add count in parens after directories + other files headings
- `c1773db` feat: add total file count to start of meta line
- `5be74b6` feat: remove `Type` column from dirs table (all entries are `DIR`)
- `94ceea0` feat: up entry as separate table above dirs table (no up-spacer row)
- `5368451` fix: make horizontal lines (header, sort-bar, section) the same width
- `97558a7` feat: whole-width section heading clickable to toggle show/hide
- `555cbde` feat: complete table row clickable for dirs + others tables
- `28abbf2` feat: section heading font bump, dir dates, up-row in table, heading divider, white sort arrow
- `0f4c100` feat: section toggle for directories + other files
- `e7d3fb8` feat: other files respond to sort selection (dirs stay alphabetical)
- `54b6841` feat: directories + other files as full-width tables with details
- `b6a227d` docs: expand JSON config section with full example + field mapping + validation
- `f8b8383` feat: `FFMPEG_PATH` env var for non-standard ffmpeg locations
- `98609c6` docs: update Lightbox controls section for Phase 65 prev/next hit areas
- `594bb5e` feat: lightbox prev/next hit areas fill window height + subtle hover tint
- `81f428b` docs: catchup for Phases 54, 55, 56, 58, 59, 60, 61
- `88ecefc` feat: video thumbnails via ffmpeg + show as lightbox poster before play
- `48544f2` fix: mobile video play button no longer advances to next media file
- `ba19cad` feat: section heading `"Images"` → `"Media"`
- `4d9c063` fix: footer synapticloop link text = `"synapticloop // image gallery"`
- `01ee18c` fix: footer synapticloop link → repo URL; remove footer border-top
- `1dc4a2b` docs: add `build-docs.sh` script + section explaining how to rebuild the PDF locally

---

## 2026-06-23

### Lightbox / scan
- `3fe7af0` (rename)

### File types / extensions
- (early extensions work)

### Initial scaffold (2026-06-13 to 2026-06-20)
The project started on 2026-06-13 as `caddy_image_gallery`. The early
commits established:

- Caddyfile module scaffold (`image_gallery` directive)
- xcaddy build script
- Lightbox overlay (image only)
- Sort bar (Name / Type / Modified / Size)
- Sort bar links preserve URL params
- Breadcrumb navigation
- Filter UI (initially single-dropdown, then multi-dropdown + Apply)
- Server-side search (`?q=`)
- Subdirs table
- Pagination
- Configurable `image_types` and `video_types`
- `page_sizes` dropdown
- Local install (`build.sh --user`)
- Font credits (SIL OFL 1.1)
- Animated light/dark fade GIF for docs

---

## Summary by category

### Features added
- Lightbox overlay with prev/next/close
- Video thumbnails (via ffmpeg)
- Subdirs table (with # Items, # Dirs, Size columns)
- Other files table
- Server-side + client-side search
- Filter UI (multi-dropdown + Apply button)
- Type filter (`?ext=`)
- Breadcrumb navigation
- Pagination (Google-style with ellipsis)
- Per-page size dropdown (configurable)
- Sort bar with arrows (Name/Type/Modified/Size)
- Click-to-sort table column headers
- EXIF metadata (lazy then eager, with sidecar cache)
- Source image/video dimensions watermark
- Hover tooltip on thumbnails
- Animated light/dark fade GIF for docs
- `no_exif` Caddyfile directive
- `max_cache_size_mb` Caddyfile directive
- `search_match` Caddyfile directive
- `path_prefix`, `root_name`, `image_types`, `video_types` directives
- Cache stats footer (hex)
- Subtle shimmer animation while loading
- Section toggle (show/hide directories, other files, media)
- Theme toggle (auto/light/dark)
- Local install via `build.sh --user [PORT]`
- `FFMPEG_PATH` env var

### Performance
- Cached source dimensions in `.meta` sidecar
- Cached EXIF data in `.exif` sidecar
- Thumb mtime = source mtime + LRU eviction via `.meta` mtime
- Stale sidecar detection via source mtime check
- EXIF sidecar in plain text format (not JSON)
- Human-Readable sidecar keys (match lightbox labels)
- Cached parsed template across requests

### Fixes
- Various template/CSS bugs (button heights, colors, hover states)
- Pagination + sort bar link state preservation
- Filter form preserving `page_size`
- Search header bug fixes (form-submitted vs JS-typed)
- `page_size=all` selection bug
- Symlink classification (Lstat vs Stat)
- Mobile layout improvements
- `no_exif` was the operator's choice for testing convenience
- `?page_size=N` URL parameter honored

### Refactors
- Project rename: `caddy_image_gallery` → `caddy_media_gallery`
- Template moved from embedded Go string to separate file with `//go:embed`
- Various naming: `num_per_page` → `page_size`, `images-section` → `media-*`
- EXIF: lazy → eager (with sidecar) → text format → Human-Readable keys
- Button labels: `Apply` → `Filter`, `All` → `Reset`

### Documentation
- SIL OFL 1.1 font credits
- ffmpeg startup detection docs
- Local install section
- Tagline `"The delightful way to serve a directory."`
- PDF refresh + cover image
- README docs updates
- Animated preview GIF
- Build script docs
- Comprehensive feature docs (catches up at 2026-06-28)
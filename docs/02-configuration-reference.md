# caddy_media_gallery — Complete configuration reference

A single-page reference for every configuration knob the
`media_gallery` Caddy handler exposes. Use this as the "what can I
configure" reference; for how to use individual features, see the
per-topic docs linked below.

For per-topic deep dives:
- [configuration.md](01-configuration.md) — Caddyfile directive details + env vars
- [templates.md](03-templates.md) — the template file + variables
- [sort-and-pagination.md](04-sort-and-pagination.md) — the URL query API

---

## 1. Caddyfile subdirectives (the primary way)

Inside an `media_gallery { ... }` block:

| Subdirective | Value | Default | Purpose |
|---|---|---|---|
| `path_prefix` | URL prefix (e.g. `images`) | directory basename | URL mount prefix used in breadcrumb links. |
| `root_name` | display name | `media root` | Display name for the root breadcrumb segment. |
| `image_types` | space-separated extensions (no leading dot) | built-in list | File extensions the gallery treats as images. |
| `video_types` | space-separated extensions (no leading dot) | built-in list | File extensions the gallery treats as videos. |
| `sort` | `mtime` / `name` (also accepts `date` as an alias for `mtime`) | `mtime` (newest first) | Default sort field. Overridable per-request via `?sort=`. |
| `page_size` | integer &gt;= 1 | (first item of `page_sizes`) | Default per-page count. Deprecated for new configs — use `page_sizes` (list form) instead. |
| `page_sizes` | space-separated list (first = default) | `60 30 120 all` | Per-page dropdown options. The first item is the default. Use the `all` token to include "show everything on one page" in the dropdown. |
| `thumb_width` | integer &gt;= 1 | `320` | Max width in pixels. Source is fit-within-bounds. |
| `thumb_height` | integer &gt;= 1 | `320` | Max height in pixels. Source is fit-within-bounds. |
| `thumb_format` | `jpeg` / `jpg` / `png` / `webp` | `webp` (lossless) | Output format. jpeg quality 75, png lossless, webp lossless. |
| `cache_scan` | integer &gt;= 1 | `1440` (24h) | Scan cache TTL in minutes. The primary invalidation is the directory mtime check on every access; the TTL is a safety net for edge cases. |
| `thumb_ttl` | integer &gt;= 1 | `1440` | HTTP `Cache-Control: max-age` for thumbs, in minutes (= 24h default). |
| `no_thumbs` | no-arg = `true` / explicit `false` = `false` | `false` (thumbs on) | Skip thumbnail generation. Tile `<img>` points to the original file. |
| `no_video_thumbs` | no-arg = `true` / explicit `false` = `false` | `false` (video thumbs on, if ffmpeg available) | Skip ffmpeg-based video poster extraction. |
| `no_exif` | no-arg = `true` / explicit `false` = `false` | `false` (EXIF on) | Disable EXIF entirely. EXIF is read LAZILY by the lightbox (via the `?exif=1` endpoint) — not at scan time. When set, the endpoint returns 404 and the lightbox EXIF panel is hidden. Privacy-friendly. |
| `search_match` | `word` / `substring` | `substring` | Filename match rule for the search feature. `word` = match the start of a word boundary. `substring` = match anywhere. |
| `template` | file name, relative to the templates dir | `gallery.tmpl` | Which template file to render. Path-traversal protected. |

**Full Caddyfile example** (every directive set):

```caddy
handle_path /images/* {
    root * /var/www/html/images
    media_gallery {
        path_prefix images
        root_name images
        image_types jpg jpeg png webp
        video_types mp4 webm
        sort name
        page_sizes 30 60 120 all
        thumb_width 480
        thumb_height 320
        thumb_format jpeg
        cache_scan 1440  # 24h — mtime check is the primary invalidation
        thumb_ttl 60
        search_match word
        max_cache_size_mb 1024
        template themes/dark/gallery.tmpl
    }
    file_server
}
```

---

## 2. JSON config fields (programmatic)

Same fields, json tags. Use `caddy adapt` or any JSON config
producer to set them:

```json
{
  "root": "/var/www/html/images",
  "path_prefix": "images",
  "root_name": "images",
  "image_types": ["jpg", "jpeg", "png", "webp"],
  "video_types": ["mp4", "webm"],
  "sort": "name",
  "page_size": 60,
  "page_sizes": ["30", "60", "120", "all"],
  "thumb_width": 480,
  "thumb_height": 320,
  "thumb_format": "jpeg",
  "cache_scan": 1440,
  "thumb_ttl": 60,
  "no_thumbs": false,
  "no_video_thumbs": false,
  "search_match": "word",
  "template": "themes/dark/gallery.tmpl"
}
```

`cache` is runtime-only (excluded from JSON via `json:"-"`).

---

## 3. Per-request URL query parameters (no config needed)

| Param | Values | Default | Effect |
|---|---|---|---|
| `sort` | `mtime` / `name` / `type` / `size` (also accepts `date` as an alias for `mtime`) | inherits from Caddyfile | Sort field |
| `order` | `asc` / `desc` | depends on `sort` | Sort direction |
| `page` | integer &gt;= 1 | `1` | Which page (only meaningful when there are > 1 pages) |
| `page_size` | any value in the operator-configured `page_sizes` list | first item | Visitor's per-page selection (driven by the dropdown). Changing this resets the visitor to page 1. Unknown values fall back to the first item. |
| `q` | free text (URL-encoded) | (none) | Server-side filename search. Combined with the visitor's `search_match` mode. Directories are never filtered. |
| `type` | comma-separated list of extensions (e.g. `jpg,png`), or the sentinel `.` for files without an extension | (none) | Server-side type filter. The form-submission version uses repeated `?ext=jpg&ext=png` (both work). Use `ext=.` (literal dot) to filter to only files with no extension (e.g. `Makefile`, `welcome`). |
| `dirs_sort` / `dirs_order` | same as the main sort | `name asc` | Sort the Directories and Other Files tables. Header click is client-side (persists in `data-search-match`-style attributes + localStorage). |

**Deliberately NOT query-overridable:**
- `?thumb_format=N` — would let users force the server to recompute thumbs in any format
- `?thumb_width=N` / `?thumb_height=N` — same

The thumb format and dimensions are set in the Caddyfile only. The page size IS now a query-overridable URL param (via the visitor's dropdown), but the operator can only set it to values in their pre-configured `page_sizes` list.

---

## 4. Environment variables

| Var | Default | Purpose |
|---|---|---|
| `GALLERY_TEMPLATES_DIR` | `/etc/caddy/gallery-templates` | Where the module looks for (and writes) the on-disk template override. **The only user-facing env var.** |

A second env var exists in the code for testing only: `GALLERY_THUMB_CACHE_DIR` (default `/var/cache/caddy-gallery`) — not documented as a user-facing knob.

See [configuration.md to Environment variables](01-configuration.md#environment-variables) for the full setup story (systemd unit, dev workflow, failure mode).

---

## 5. Caddyfile handler-level wiring (external, not `media_gallery`-specific)

These live in the surrounding Caddyfile, not inside the `media_gallery { ... }` block:

| Caddyfile construct | Purpose | Example |
|---|---|---|
| `handle_path` (or `route`) | Strips URL prefix and routes to `media_gallery` | `handle_path /images/* { ... }` |
| `root *` | Per-request "root" var, read by `media_gallery` to find the directory to scan | `root * /var/www/html/images` |
| `file_server` | Fallthrough for direct file requests and 404s (must come AFTER `media_gallery`) | `file_server` |
| Auth (Authelia, basic_auth, etc.) | Wraps the route in auth | `(auth)` snippet (or `basicauth { ... }` block) |

---

## 6. External: the Caddyfile as a whole

The live site typically looks like:

```caddy
hermes.synapticloop.com {
    import auth
    route /images/* {
        root * /var/www/html/images
        media_gallery
        file_server
    }
}
```

The `(auth)` snippet is defined elsewhere in the Caddyfile and wraps every route in Authelia `forward_auth` at `127.0.0.1:9091`.

---

## 7. Behavior modifiers (not configurable, hardcoded)

These are baked into the source. They could be promoted to config
options if a use case comes up, but currently aren't:

| Constant | Value | Where |
|---|---|---|
| Thumb JPEG quality (when `thumb_format jpeg`) | 75 | `thumbnails.go` |
| Video thumb extraction | uses `ffmpeg -vframes 1` (first frame), scaled to `thumb_width` × `thumb_height` | `thumbnails.go` |
| Video thumb max retries | 1 (no retry; failed extraction leaves the placeholder) | `thumbnails.go` |
| Lightbox | page-scoped (visits the current page only, not all pages in the directory) | `render.go` (lightbox JS in the template) |
| EXIF fields extracted | CAMERA subset only (Make, Model, Lens, DateTimeOriginal, ExposureTime, FNumber, ISOSpeedRatings, FocalLength). GPS data is never read. | `exif.go` |
| EXIF parser | `github.com/dsoprea/go-exif/v3` (supports JPEG, PNG, WebP) | `exif.go` |
| Search match mode default | `substring` (most permissive; the operator can opt into `word` via `search_match word`) | `exif.go` + `gallery.go` (Provision) |
| Dimensions reading | `image.DecodeConfig` (images, header-only) + `ffprobe` (videos, 10s timeout) | `dimensions.go` |
| Per-page dropdown default list | `60 30 120 all` (first item is the default) | `gallery.go` (Provision) |
| Page-size reset behaviour | changing the page size via the dropdown resets the visitor to page 1 (current `?page=` is dropped from the form's hidden inputs) | `render.go` (page-size form) |
| Other-files strip on a subdir page | "Other files" rendered as a table above the image grid, with click-to-sort headers | `render.go` (splitFiles) |
| Directories strip on every page | Always rendered, alphabetical, ignores sort. Has click-to-sort headers too (name / # items / # sub-dirs / size / modified). | `render.go` (splitFiles + dirs sort) |
| Lightbox EXIF panel | shows Camera, Lens, Date, Exposure (Shutter · Aperture · ISO · Focal) — only when the image has EXIF | `render.go` (lightbox JS) |
| Thumbnail loading indicator | subtle diagonal shimmer while the thumbnail image loads, removed on `load` / `error` events | `render.go` (inline JS) |

---

## 8. Quick reference — what's where

If you're wondering "where is this knob?":

| Want to configure... | Use |
|---|---|
| Sort field (default + per-request) | `sort` directive + `?sort=` query |
| Sort direction | `?order=` query (`asc` / `desc`) |
| Pagination | `page_size` (default) + `page_sizes` (dropdown list) + `?page=` query |
| Per-page dropdown selection | `?page_size=` query (validated against `page_sizes` list; resets to page 1 on change) |
| Type filter (server-side) | `?type=jpg,png` or repeated `?ext=jpg&ext=png` |
| Filename search (server-side) | `?q=foo` (combined with the visitor's `search_match` mode) |
| Search match mode | `search_match` directive (`word` or `substring`, default `substring`) |
| Thumbnail on/off | `no_thumbs` directive |
| Video thumbnail on/off | `no_video_thumbs` directive |
| Thumbnail size | `thumb_width` / `thumb_height` directives |
| Thumbnail format (jpeg/png/webp) | `thumb_format` directive |
| Thumbnail browser cache TTL | `thumb_ttl` directive |
| Directory scan cache TTL | `cache_scan` directive |
| Image extensions | `image_types` directive (space-separated) |
| Video extensions | `video_types` directive (space-separated) |
| URL mount prefix (for breadcrumb) | `path_prefix` directive |
| Root breadcrumb display name | `root_name` directive |
| Template file (theme) | `template` directive (e.g. `themes/dark/gallery.tmpl`) |
| Templates directory (where to find templates) | `GALLERY_TEMPLATES_DIR` env var |
| Thumbnails cache directory | `GALLERY_THUMB_CACHE_DIR` env var (testing only) |
| EXIF parsing | automatic (no config). CAMERA fields only; GPS data NEVER read. | `exif.go` |
| Source dimensions in thumbnail | automatic (no config). Bottom-left watermark on every card. | `dimensions.go` |
| Auth (who can view) | Caddyfile `basicauth` or `(auth)` snippet (external) |
| Route (URL prefix) | Caddyfile `handle_path` or `route` (external) |
| Image directory | Caddyfile `root *` (external) |

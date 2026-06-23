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
| `sort` | `mtime` / `name` (also accepts `date` as an alias for `mtime` — see [Sort & Pagination to Aliases](04-sort-and-pagination.md#aliases)) | `mtime` (newest first) | Default sort field. Overridable per-request via `?sort=`. |
| `template` | file name, relative to the templates dir | `gallery.tmpl` | Which template file to render. Path-traversal protected. |
| `no_thumbs` | no-arg = `true` / explicit `false` = `false` | `false` (thumbs on) | Skip thumbnail generation. Tile `<img>` points to the original file. |
| `page_size` | integer &gt;= 1 | `50` | Image entries per page. Nav only renders when `total pages > 1`. |
| `thumb_width` | integer &gt;= 1 | `320` | Max width in pixels. Source is fit-within-bounds. |
| `thumb_height` | integer &gt;= 1 | `320` | Max height in pixels. Source is fit-within-bounds. |
| `thumb_format` | `jpeg` / `jpg` / `png` / `webp` | `webp` (lossless) | Output format. jpeg quality 75, png lossless, webp lossless. |
| `cache_scan` | integer &gt;= 1 | `1` | Scan cache TTL in minutes. |
| `thumb_ttl` | integer &gt;= 1 | `1440` | HTTP `Cache-Control: max-age` for thumbs, in minutes (= 24h default). |

**Full Caddyfile example** (every directive set):

```caddy
handle_path /images/* {
    root * /var/www/html/images
    media_gallery {
        sort name
        template themes/dark/gallery.tmpl
        no_thumbs false
        page_size 100
        thumb_width 480
        thumb_height 320
        thumb_format jpeg
        cache_scan 5
        thumb_ttl 60
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
  "sort": "name",
  "template": "themes/dark/gallery.tmpl",
  "no_thumbs": false,
  "page_size": 100,
  "thumb_width": 480,
  "thumb_height": 320,
  "thumb_format": "jpeg",
  "cache_scan": 5,
  "thumb_ttl": 60
}
```

`cache` is runtime-only (excluded from JSON via `json:"-"`).

---

## 3. Per-request URL query parameters (no config needed)

| Param | Values | Default | Effect |
|---|---|---|---|
| `sort` | `mtime` / `name` / `type` / `size` (also accepts `date` as an alias for `mtime` — see [Sort & Pagination to Aliases](04-sort-and-pagination.md#aliases)) | inherits from Caddyfile | Sort field |
| `order` | `asc` / `desc` | depends on `sort` | Sort direction |
| `page` | integer &gt;= 1 | `1` | Which page (only meaningful when `page_size` causes pagination) |

**Deliberately NOT query-overridable:**
- `?page_size=N` — would let users request arbitrarily large pages and could DOS the server
- `?thumb_format=N` — would let users force the server to recompute thumbs in any format
- `?thumb_width=N` — same

The page size, format, and thumb dimensions are set in the Caddyfile only.

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
| Video thumbs | not generated (videos show play-button overlay in the template) | `thumbnails.go` (source extensions are image-only) |
| Lightbox | page-scoped (50 images on the current page, not all 1,197) | `render.go` (lightbox JS in the template) |
| Other-files strip on a subdir page | "Other files" rendered as horizontal chips above the image grid | `render.go` (splitFiles) |
| Directories strip on every page | Always rendered, alphabetical, ignores sort | `render.go` (splitFiles + dirs sort) |

---

## 8. Quick reference — what's where

If you're wondering "where is this knob?":

| Want to configure... | Use |
|---|---|
| Sort field (default + per-request) | `sort` directive + `?sort=` query |
| Pagination | `page_size` directive + `?page=` query |
| Thumbnail on/off | `no_thumbs` directive |
| Thumbnail size | `thumb_width` / `thumb_height` directives |
| Thumbnail format (jpeg/png/webp) | `thumb_format` directive |
| Thumbnail browser cache TTL | `thumb_ttl` directive |
| Directory scan cache TTL | `cache_scan` directive |
| Template file (theme) | `template` directive (e.g. `themes/dark/gallery.tmpl`) |
| Templates directory (where to find templates) | `GALLERY_TEMPLATES_DIR` env var |
| Thumbnails cache directory | `GALLERY_THUMB_CACHE_DIR` env var (testing only) |
| Auth (who can view) | Caddyfile `basicauth` or `(auth)` snippet (external) |
| Route (URL prefix) | Caddyfile `handle_path` or `route` (external) |
| Image directory | Caddyfile `root *` (external) |

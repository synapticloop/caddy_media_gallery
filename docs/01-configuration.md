# Configuration

## Caddyfile directive

```caddy
handle_path /images/* {
    root * /var/www/html/images
    image_gallery
    file_server
}
```

The `image_gallery` directive accepts one inline option:

| Subdirective | Value | Default | Purpose |
|---|---|---|---|
| `template` | file name, relative to the templates dir | `gallery.tmpl` | Pick which template file to render. Path-traversal protected: no `..`, no absolute paths — the templates dir is a chroot. |
| `no_thumbs` | `true` / `false` (no-arg = `true`) | `false` (thumbs on) | Skip on-the-fly WebP thumbnail generation for **images**. Tile `<img src>` points to the original file instead of `~/_thumbs/<name>.webp`. Thumb requests fall through to the next handler. Useful for small galleries where you don't want a thumb cache. See `no_thumbs` walkthrough below. |
| `no_video_thumbs` | `true` / `false` (no-arg = `true`) | `false` (video thumbs on, if ffmpeg available) | Skip on-the-fly WebP thumbnail generation for **videos** (extracted from the first frame via ffmpeg). When `true`, videos still display in the gallery (with the placeholder gradient + play button on each tile) but no per-frame thumbnail is generated. When `false` (default), video thumbs ARE generated IF ffmpeg is available on the host. If ffmpeg is missing, video thumbs fall back to the placeholder regardless of this setting (we can't decode a frame without a tool that can). Use `no_video_thumbs` to skip the ffmpeg invocation even when it's available (e.g., on hosts where you don't want the CPU cost of frame extraction). See "Video thumbnails (ffmpeg)" below. |
| `page_size` | integer &gt;= 1 | `50` | How many image entries to show per page. Must be a positive integer; `page_size 0` is rejected (use no directive, or set the explicit value you want). The pagination nav only renders when total pages > 1, so a 30-image gallery at the default 50 shows all 30 on a single page with no nav. |

Example with a themed subdir:

```caddy
handle_path /images/* {
    root * /var/www/html/images
    image_gallery {
        template themes/dark/gallery.tmpl
    }
    file_server
}
```

This loads `$GALLERY_TEMPLATES_DIR/themes/dark/gallery.tmpl` (or
falls back to the bundled template if the file doesn't exist on
disk). The path is validated at Provision — an invalid name
(e.g. `../etc/passwd`) fails Caddy startup, not at first request.

### Example: skip thumbnail generation

```caddy
handle_path /images/* {
    root * /var/www/html/images
    image_gallery {
        no_thumbs
    }
    file_server
}
```

With `no_thumbs`:
- Each tile's `<img src>` is the original image file (`./photo.jpg`), not `~/_thumbs/photo.webp`
- No thumb generation, no cache, no CPU cost on first request
- The browser downloads the full image per tile (bigger page payload, slower on dirs of large photos)

Use `no_thumbs false` to turn it back on (the default is off, so the directive is opt-in).

### Example: disable video thumbnails

Video thumbnails (per the table above) require ffmpeg. If ffmpeg isn't available, the gallery falls back to the placeholder gradient + play button automatically — you don't need to do anything. If ffmpeg IS available and you want to skip frame extraction (e.g., on a low-CPU host or for very large videos), use `no_video_thumbs`:

```
image_gallery {
    no_video_thumbs
}
```

With this, video tiles show the placeholder gradient + play button (no `<img>` for the frame). The same `no_video_thumbs false` form re-enables frame extraction.

The video thumb generation uses `ffmpeg -vframes 1` to extract the first frame, scaled to fit the configured `thumb_width` × `thumb_height` (defaults 320×320). The output is a WebP, written to the same cache dir as image thumbs (`/var/cache/caddy-gallery` by default, override via `GALLERY_THUMB_CACHE_DIR`). Same caching rules as image thumbs (regenerate only when the source video's mtime is newer than the cache file).

If the operator has ffmpeg installed at a non-standard path (not in `$PATH`), set the `FFMPEG_PATH` env var to the absolute path of the ffmpeg binary. This is checked first (Phase 67); if unset or the file isn't executable, the code falls back to `exec.LookPath("ffmpeg")` which scans `$PATH`.

```
FFMPEG_PATH=/opt/ffmpeg-7/bin/ffmpeg caddy run
```

The `FFMPEG_PATH` value is validated at Provision time: it must point to an existing regular file with at least one executable bit set. Bad values (non-existent path, directory, non-executable file) are silently ignored and the code falls back to `$PATH` lookup. This avoids a confusing "exec: not found" error at request time when the env var is mistyped.

All standard install paths (`/usr/bin/ffmpeg`, `/usr/local/bin/ffmpeg`, etc.) are picked up automatically via the `$PATH` fallback.
Best for: small galleries (< 100 images) where you don't want a thumb cache and the originals aren't huge. Not recommended for large galleries — the page payload goes from ~30 KB (with thumbs) to ~5 MB average (full images) for a 1,000-image dir.

### Example: change the page size

```caddy
handle_path /images/* {
    root * /var/www/html/images
    image_gallery {
        page_size 100
    }
    file_server
}
```

This shows 100 image entries per page instead of the default 50. Tradeoffs: larger pages mean fewer HTTP requests, but each request returns a bigger HTML payload (and the server uses more memory per render). The pagination nav at the bottom of the page only renders when total pages > 1, so if your gallery has 30 images and you set `page_size 100`, you get all 30 on one page with no nav. URL query override: append `?page=2` to the gallery URL to jump to a specific page. `?page_size=N` is NOT a query param — page size is set in the Caddyfile only (per-request override would let the user request arbitrarily large pages and could DOS the server).

All other configuration (the `root *` for the image directory,
the `handle` / `handle_path` for the route, the auth wrapper) is
via the surrounding Caddyfile block, not the `image_gallery`
directive. The module reads the on-disk directory from Caddy's
per-request `root` variable, or from the `Root` JSON field if
set explicitly.

## JSON config (advanced)

Caddy supports two configuration formats: the **Caddyfile** (text,
what most examples on this page use) and **JSON** (the native
config format Caddy uses internally). Every Caddyfile gets
converted to JSON before being applied — but you can also write
JSON directly, which is useful for:

- **Programmatic / templated config** (Kubernetes, Terraform,
  Ansible) — JSON can be generated from variables
- **Many Caddy instances** — JSON is diffable, lintable, and
  can be validated in CI pipelines
- **Dynamic reload** — `caddy reload` accepts JSON via the admin
  API (`curl -X POST http://localhost:2019/load`)
- **Sharing snippets** — unambiguous quoting (no special-character
  rules like Caddyfile)

### Minimum JSON config

The minimum handler block (only `handler` is required):

```json
{
  "handler": "image_gallery",
  "root": "/var/www/html/images"
}
```

The `Root` field is optional — the module falls back to the
per-request `root` set by the surrounding `handle` / `handle_path`
block. Set it explicitly only if you need to override the
request-time root.

### Full JSON config (all fields)

Here's a complete JSON config showing every configurable field
of the `image_gallery` handler, with realistic values:

```json
{
  "handler": "image_gallery",
  "root": "/var/www/html/images",
  "sort": "name",
  "template": "gallery.tmpl",
  "no_thumbs": false,
  "no_video_thumbs": false,
  "page_size": 50,
  "thumb_width": 320,
  "thumb_height": 320,
  "thumb_format": "webp",
  "thumb_ttl": 1440,
  "cache_scan": 1
}
```

All fields are optional except `handler` (always required).
Defaults match the Caddyfile defaults — if you omit a field,
the same default value applies.

### Caddyfile ↔ JSON field mapping

| Caddyfile directive | JSON field | Type | Default |
|---|---|---|---|
| `root` (per-handle) | `"root"` | string | (per-request) |
| `sort <name\|mtime\|type\|size>` | `"sort"` | string | `"mtime"` |
| `template <name>` | `"template"` | string | `"gallery.tmpl"` |
| `no_thumbs` | `"no_thumbs"` | bool | `false` |
| `no_video_thumbs` | `"no_video_thumbs"` | bool | `false` |
| `page_size <N>` | `"page_size"` | int | `50` |
| `thumb_width <N>` | `"thumb_width"` | int | `320` |
| `thumb_height <N>` | `"thumb_height"` | int | `320` |
| `thumb_format <webp\|jpeg\|png>` | `"thumb_format"` | string | `"webp"` |
| `thumb_ttl_minutes <N>` | `"thumb_ttl"` | int | `1440` (24h) |
| `cache_scan_minutes <N>` | `"cache_scan"` | int | `1` |

**Heads up on the JSON naming:** the JSON field names use the
`json:"name"` struct tags — for `CacheScanMinutes` the tag is
`cache_scan` (not `cache_scan_minutes`), and for `ThumbTTLMinutes`
it's `thumb_ttl` (not `thumb_ttl_minutes`). This is intentional:
the Go struct tags are short, and the Caddyfile subdirectives
keep the verbose names. The mapping table above is authoritative
for what JSON field names to use — the Go field names are an
implementation detail.

The mapping for the other fields is mechanical: every Caddyfile
subdirective has a matching JSON field with the same name
(snake_case throughout). The Go struct field names are `Root`,
`Sort`, etc. (PascalCase), but the JSON tags normalize them to
snake_case via the `json:"name,omitempty"` struct tags.

### Validation

You can validate a JSON config without starting Caddy:

```
caddy validate --config /etc/caddy/caddy.json
```

Output: `Valid configuration` (or a JSON parse error pointing
to the problem). The `caddy validate` command works for both
JSON and Caddyfile inputs — for Caddyfile, just point it at the
file directly.

### Dynamic reload

Push a JSON config to a running Caddy via the admin API:

```
curl -X POST http://localhost:2019/load \
  -H "Content-Type: application/json" \
  --data-binary @new-config.json
```

Returns `200 OK` on success, or a JSON error response on failure.
This is what `caddy reload` does internally — but you can also
script reloads (e.g., update the gallery config when the disk
layout changes) by POSTing to this endpoint.

### When to use JSON vs Caddyfile

For **single-host, edit-by-hand** use, Caddyfile is the right tool:
simpler for humans, more forgiving of whitespace and comments,
easier to read for someone not familiar with the schema. This is
what most of this document shows.

For **automated / multi-instance / programmatic** use, JSON is
the right tool: it's diffable, lintable, validatable, and can be
generated from templates. This is what Caddy uses internally,
and what most CI/CD pipelines produce.

You can also convert between them: `caddy adapt --config Caddyfile`
produces JSON on stdout, and `caddy adapt --config caddy.json --adapter caddyfile`
goes the other way (rare, since the Caddyfile syntax is
less expressive).

## Environment variables

The plugin reads **one** environment variable:

### `GALLERY_TEMPLATES_DIR`

**Where it's read in code:** `render.go`, in two functions —
`loadTemplate()` (at request time, to find the on-disk override)
and `writeBundledTemplates()` (at Caddy startup, to write the
bundled templates so operators can see them). Both do the same
thing:

```go
dir := os.Getenv("GALLERY_TEMPLATES_DIR")
if dir == "" {
    dir = "/etc/caddy/gallery-templates"
}
```

**Default:** `/etc/caddy/gallery-templates`. Created on first
Caddy startup by `writeBundledTemplates()` with mode 0755, owned
by whatever user the Caddy systemd service runs as. If the
template file already exists, it's left alone (operator overrides
are preserved across restarts).

**The directory it points to:** the absolute path you set. The
plugin does **not** create parent directories beyond the templates
dir itself — it just `MkdirAll`s the final directory. So if you
point it at `/srv/gallery-templates`, that path needs to be
writable by the Caddy process.

**How to set it for the live Caddy** (the canonical way —
systemd starts the process with a clean environment, so your
shell's env doesn't reach the service):

```bash
# One-off (persists in the manager's env, inherited by all units)
sudo systemctl set-environment GALLERY_TEMPLATES_DIR=/etc/caddy/gallery-templates
sudo systemctl restart caddy

# Or persistently in the caddy service unit
sudo systemctl edit caddy
# Add:
#   [Service]
#   Environment="GALLERY_TEMPLATES_DIR=/etc/caddy/gallery-templates"
# Or use EnvironmentFile= pointing at a file with the line
sudo systemctl daemon-reload
sudo systemctl restart caddy
```

**How to set it for dev** (`go test`, `xcaddy build`, or running
a custom Caddy from your shell):

```bash
export GALLERY_TEMPLATES_DIR=/path
# then run your commands
```

Note: the test suite sets `GALLERY_TEMPLATES_DIR` to a temp dir
via `TestMain`, so `go test` is isolated from your shell's value.
The module always reads the env at request time, so changes to
the env var take effect on the next Caddy restart — no rebuild
needed.

**What happens if the dir is unwritable or doesn't exist:**
`writeBundledTemplates()` logs a warning to stderr and continues.
The bundled templates still serve fine (the on-disk file is a
convenience, not a requirement). The next request falls back to
the bundled constant.

**There are no other env vars.** Sort order, pagination, page
size, and the thumb cache directory are all configurable in code
(constants in the source) rather than via env vars.

## In-code constants (not configurable)

These are baked into the source. They can be changed by editing the
code, rebuilding, and restarting Caddy — but they're not exposed
as Caddyfile directives or env vars. Listed here so you know what
the defaults are and where to find them.

| Constant | Value | Where |
|---|---|---|
| Thumbnail size | 320px | `thumbnails.go` |
| Thumb `Cache-Control` max-age | 86400 (24h) | `thumbnails.go` |
| Thumb format | WebP (lossless, via `nativewebp`) | `thumbnails.go` |
| Scan cache TTL | 1 minute | `gallery.go` (`NewScanCache(time.Minute)`) |
| Thumb width (px) | 320 | `thumbnails.go` (the resize) |

**Note:** "Thumbnail size" and "Thumb width (px)" both reference
the same constant (320) — the maximum dimension of the
generated thumbnail. If you wanted them to be different values
(say, max-dim 320 and width 280 for a non-square crop), let me
know and I'll split them into two separate constants.

**Why these aren't configurable yet:** most operators never need
to change them. The thumb size and Cache-Control max-age are
sensible defaults that work for the vast majority of galleries.
The scan cache TTL is short enough (1 minute) that new files
appear quickly without manual cache busting, and long enough
that hot directories don't re-scan on every request. If you find
yourself wanting to change one of these, that's a signal that
the constant should probably be promoted to a config option —
file an issue and we can discuss.

## What `image_gallery` does NOT do

- It does **not** generate thumbnails at build time — thumbnails
  are generated on-the-fly on first request and cached in
  `/var/cache/caddy-gallery/`. See the wiki page for cache
  details.
- It does **not** handle uploads or modifications. It's
  read-only.
- It does **not** support nested directory pagination — when you
  click into a subdirectory, that subdirectory has its own
  gallery with its own pagination, but the parent dir's image
  list isn't merged in.
- It does **not** redirect from `/foo` to `/foo/`. If you request
  `/images/generated` (no trailing slash) and it's a directory,
  the module returns a 301 with a **relative** `Location: generated/`
  header (so the browser resolves it relative to the current
  URL). This is a workaround for Caddy's `handle_path` rewriting
  both `r.URL.Path` and `r.RequestURI`, which makes absolute
  reconstruction impossible inside the handler.

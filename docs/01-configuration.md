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
| `no_thumbs` | `true` / `false` (no-arg = `true`) | `false` (thumbs on) | Skip on-the-fly WebP thumbnail generation. Tile `<img src>` points to the original file instead of `~/_thumbs/<name>.webp`. Thumb requests fall through to the next handler. Useful for small galleries where you don't want a thumb cache. See `no_thumbs` walkthrough below. |
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
- Requests to `~/_thumbs/<name>.webp` fall through to the next handler (file_server), which 404s

Best for: small galleries (< 100 images) where you don't want a thumb cache and the originals aren't huge. Not recommended for large galleries — the page payload goes from ~30 KB (with thumbs) to ~5 MB average (full images) for a 1,000-image dir.

Use `no_thumbs false` to turn it back on (the default is off, so the directive is opt-in). Videos are unaffected either way (they don't have thumbs — they show a play-button overlay).

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

Equivalent JSON, if you're configuring Caddy via the admin API or
a config file rather than a Caddyfile:

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

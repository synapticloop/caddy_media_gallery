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

| Variable | Default | Purpose |
|---|---|---|
| `GALLERY_TEMPLATES_DIR` | `/etc/caddy/gallery-templates` | Where the module looks for (and writes) on-disk template overrides. See [Templates](templates.md). |

There are no other env vars. Sort order, pagination, and the
thumb cache directory are all configurable in code (constants in
the source) rather than via env vars.

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

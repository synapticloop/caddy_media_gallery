# Configuration

## Caddyfile directive

```caddy
handle_path /images/* {
    root * /var/www/html/images
    image_gallery
    file_server
}
```

The `image_gallery` directive takes no inline options. All
configuration is via the surrounding `handle` / `handle_path` block
(notably `root *` — the module reads the on-disk directory from
Caddy's per-request `root` variable, or from the `Root` JSON
field if set explicitly).

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

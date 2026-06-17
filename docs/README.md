# caddy-image-gallery — Documentation

A Caddy v2 HTTP handler module that replaces Caddy's default
`file_server browse` with a self-contained, image-aware gallery:
thumbnails, click-to-expand lightbox, sortable + paginated image grid,
and a separate "Other files" strip above.

## Index

| Doc | What it covers |
|---|---|
| [Configuration](configuration.md) | The `image_gallery` Caddyfile directive, JSON config, env vars |
| [Templates](templates.md) | How the templates work, what variables you can use, how to customize |
| [Sort & Pagination](sort-and-pagination.md) | The `?sort=`, `?order=`, `?page=` URL API |

## Quick start

In your Caddyfile:

```caddy
handle_path /images/* {
    root * /var/www/html/images
    image_gallery
    file_server
}
```

Build with xcaddy:

```bash
xcaddy build \
    --with github.com/caddyserver/caddy@v2.11.4 \
    --with github.com/synapticloop/caddy_image_gallery@latest
```

Hit it: `https://your-host/images/`. You get a paginated, sortable
image grid with thumbnails, click-to-expand lightbox, an
"open in new tab" button per tile, and a directory strip at the
top for navigation. Direct file requests (`/images/photo.jpg`)
fall through to `file_server` so the originals serve as-is.

## What's where

- **Project root:** `~/projects/caddy_image_gallery/` (wherever you cloned it — adjust paths below to match)
- **Templates dir (auto-created on first startup):**
  `/etc/caddy/gallery-templates/` — see [Templates](templates.md)
- **Thumb cache:** `/var/cache/caddy-gallery/<sha256>.webp`
- **Plan / design:** `~/.hermes/plans/2026-06-13_154500-caddy_image_gallery.md`
- **Wiki page:** `~/.wiki/projects/caddy_image_gallery.md`

# caddy-media-gallery — Documentation

A Caddy v2 HTTP handler module that replaces Caddy's default
`file_server browse` with a self-contained, image and video 
aware gallery - built for ease of visualisation:

 - Choose your mode, light, dark, or auto
 - Status line with an overview of the directory
 - Sort files by name, type, date modified, or size
 - Parent directory link
 - List of sub-directories
 - List of other - non-media files
 - Individual tiles for media files
   - thumbnails for images,
   - play button placeholders for videos
 - click-to-expand lightbox, or open the media in a new tab
 - 
 - and a separate "Other files" strip above,
 - temaplates so that you can roll your own
 - light and dark modes (including matching the system defaults)
 - pagination (configurable number of results per page)
 - Sortable

## Index

| Doc | What it covers |
|---|---|
| [Configuration reference](02-configuration-reference.md) | One-page index of every config knob (directives, JSON, env vars, query params, in-code constants) |
| [Configuration](01-configuration.md) | The `media_gallery` Caddyfile directive, JSON config, env vars |
| [Templates](03-templates.md) | How the templates work, what variables you can use, how to customize |
| [Sort & Pagination](04-sort-and-pagination.md) | The `?sort=`, `?order=`, `?page=` URL API |

## Quick start

In your Caddyfile:

```caddy
handle_path /images/* {
    root * /var/www/html/images
    media_gallery
    file_server
}
```

Build with xcaddy:

```bash
xcaddy build \
    --with github.com/caddyserver/caddy@v2.11.4 \
    --with github.com/synapticloop/caddy_media_gallery@latest
```

Or use the included build script (pinned versions, also
restarts Caddy via systemd):

```bash
./build.sh            # system install (needs sudo)
./build.sh --check    # build into ./caddy without installing (CI)
./build.sh --user     # local install, no sudo; builds to ~/bin/caddy
./build.sh --help     # full usage
```

### Local install (no root, no sudo)

If you don't have sudo access (shared host, locked-down
laptop, etc.), use `--user`. It builds the binary into
`~/bin/caddy`, generates a starter `Caddyfile.user` in
the project root, and validates the port (>1024).

```bash
# Default: build to ~/bin/caddy, listen on port 3245 (0xCAD)
# serve ~/Pictures.
./build.sh --user

# Custom port and root directory:
CADDY_USER_PORT=9000 CADDY_USER_ROOT=~/photos ./build.sh --user

# Start it (foreground, Ctrl+C to stop):
~/bin/caddy run --config Caddyfile.user

# Or in the background:
nohup ~/bin/caddy run --config Caddyfile.user > ~/caddy.log 2>&1 &
echo $! > ~/caddy.pid
```

The auto-generated `Caddyfile.user` uses `admin off`
and `http://` (no TLS, no ACME) — both would need
extra setup for a fully no-sudo install, neither is
needed for local dev. To override the auto-generated
file, just edit it; subsequent builds leave it alone.

The bundled template (default `gallery.tmpl`) is used
automatically when no on-disk override is provided
— no need for `GALLERY_TEMPLATES_DIR` setup either.

Hit it: `https://your-host/images/` (or
`http://localhost:3245/` for `--user`). You get a
paginated, sortable image grid with thumbnails,
click-to-expand lightbox, an "open in new tab" button
per tile, and a directory strip at the top for
navigation. Direct file requests (`/images/photo.jpg`)
fall through to `file_server` so the originals serve
as-is.

## What's where

- **Project root:** `~/projects/caddy_media_gallery/` (wherever you cloned it — adjust paths below to match)
- **Templates dir (auto-created on first startup):**
  `/etc/caddy/gallery-templates/` — see [Templates](03-templates.md)
- **Thumb cache:** `/var/cache/caddy-gallery/<sha256>.webp`
- **Plan / design:** `~/.hermes/plans/2026-06-13_154500-caddy_media_gallery.md`
- **Wiki page:** `~/.wiki/projects/caddy_media_gallery.md`

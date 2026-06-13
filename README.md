# caddy-image-gallery

A Caddy v2 HTTP handler module that renders a directory as a dark-themed
image gallery. Replaces Caddy's default `file_server browse` with a
thumbnailed grid, click-to-expand lightbox, and "Other files" section for
non-image content.

![preview](#) <!-- TODO: add a screenshot if you have one handy -->

## Features

- **Drop-in replacement** for `file_server browse` in a `handle_path` block.
- **Recursive** — every subdirectory under the matched route is rendered as a gallery.
- **WebP thumbnails** generated on the fly, cached on disk, invalidated by source mtime.
- **Vanilla JS lightbox** for click-to-expand, no external JS dependencies.
- **Dark/noir theme** with monospace headers and cool blue accent.
- **Native `loading="lazy"`** on every thumbnail.
- **Video support** — videos show a play-button overlay and link to the raw file.
- **"Other files" section** for non-image/non-video content in a directory.

## Install

Build a custom Caddy binary with this module baked in:

```bash
xcaddy build \
    --with github.com/caddyserver/caddy@v2.11.4 \
    --with github.com/synapticloop/caddy-image-gallery@latest
```

Or use the included build script (pins Caddy to v2.11.4 and the local module path):

```bash
./build.sh
```

The build script also restarts Caddy via systemd (you may need to be root or use sudo).

## Caddyfile usage

```caddyfile
handle_path /images/* {
    root * /var/www/html/images
    image_gallery         # default: mtime desc, 320px WebP thumbs
    file_server           # serves direct file requests (e.g. /images/foo.jpg)
}

# Or with explicit sort:
handle_path /images/crosswords/* {
    root * /var/www/html/images/crosswords
    image_gallery { sort name }   # alphabetical for curated content
}
```

The `image_gallery` directive MUST come before `file_server` in the handle block — that way it gets a chance to handle the request (gallery HTML, thumbnail requests), and only falls through to `file_server` for direct file access (e.g. `/images/foo.jpg`).

### Auth

The gallery slots behind any standard Caddy auth layer (basic_auth, forward_auth, JWT, etc.) — it's just a regular HTTP handler. It does not implement its own auth.

## Caddyfile directive options

| Option | Default | Description |
|--------|---------|-------------|
| `sort`  | `mtime` | `mtime` (newest first by modification time) or `name` (alphabetical) |

Example:
```caddyfile
image_gallery { sort name }
```

## How thumbs work

Thumb URLs look like `/_thumbs/<basename>.webp` (e.g. for source `photo.jpg`, the thumb is at `/_thumbs/photo.webp`). On first request, the module:

1. Hashes the source's absolute path (sha256, first 16 bytes).
2. Checks the cache at `/var/cache/caddy-gallery/<hash>.webp`.
3. If the cached file's mtime is older than the source, regenerates:
   - Decode source (jpg, png, gif, webp via stdlib + golang.org/x/image)
   - Resize to 320px wide, preserve aspect ratio
   - Encode as lossless WebP (VP8L) using github.com/HugoSmits86/nativewebp
   - Write to cache, return the bytes
4. Subsequent requests serve the cached file directly.

Cache invalidation is purely mtime-based — no cron job, no inotify watcher.

**Cache directory** is `/var/cache/caddy-gallery` by default. Override with the `GALLERY_THUMB_CACHE_DIR` env var (useful for testing).

## Caching & performance

- **Scan cache** — each directory is scanned at most once per minute (mtime-keyed). For 100+ image directories like `/images/generated/`, this drops per-request work from milliseconds to microseconds.
- **Thumb cache** — WebP thumbs are written to disk and served from disk; subsequent requests are a single `os.ReadFile`. The thumb URL is content-addressed (sha256 of the source path), so the URL itself is cacheable.
- **HTTP `Cache-Control: public, max-age=86400`** on thumb responses (24h, since thumbs are immutable per source mtime).
- **HTTP `Cache-Control: no-cache`** on gallery HTML (so newly-added images show up on the next refresh).

## Dependencies

- [caddyserver/caddy](https://github.com/caddyserver/caddy) v2.11.4 (compile-time)
- [golang.org/x/image](https://pkg.go.dev/golang.org/x/image) — for image resizing
- [HugoSmits86/nativewebp](https://github.com/HugoSmits86/nativewebp) — pure-Go lossless WebP encoder (no CGO, no libwebp)

## Build

```bash
# Clone
git clone https://github.com/synapticloop/caddy-image-gallery
cd caddy-image-gallery

# Build (requires xcaddy and Go 1.21+)
go mod download
./build.sh
```

## Test

```bash
go test ./... -v
go test ./... -race       # race detector
```

24 tests, all standard library + stdlib-friendly patterns. No test fixtures in the repo — the test for thumbnail generation uses a programmatically-generated 640x480 JPEG.

## Architecture

```
caddy-image-gallery/
├── gallery.go          # Module registration, Caddyfile parser, ServeHTTP
├── scanner.go          # Directory walker + file classification (image/video/other)
├── scancache.go        # mtime-keyed in-memory cache of directory scans
├── render.go           # HTML template + inlined dark CSS + inlined lightbox JS
├── thumbnails.go       # WebP thumb generation (decode → resize → encode), mtime cache
├── *_test.go           # Go tests (24 total)
├── build.sh            # xcaddy build + systemd restart
└── README.md           # this file
```

## Caddyfile example (full)

```caddyfile
{
    admin off
}

your.caddy.host:443 {
    tls /etc/caddy/caddy.crt /etc/caddy/caddy.key

    route {
        basic_auth {
            youruser $2a$14$bcrypt_hash_here
        }

        handle_path /images/* {
            root * /var/www/html/images
            image_gallery
            file_server
        }
    }
}
```

## License

MIT. See [LICENSE](LICENSE) (add one if you haven't — the module code is yours to license).

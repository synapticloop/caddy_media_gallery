# Configuration

## Caddyfile directive

The production setup — Caddy running as a system service,
serving a site via HTTPS:

```caddy
handle_path /images/* {
    root * /var/www/html/images
    media_gallery
    file_server
}
```

For a local-dev setup (no sudo, no TLS), see the "Local
install" subsection below — the Caddyfile is much
simpler (just `http://localhost:3245` instead of a TLS
hostname, no `admin` block needed for the bundled Caddyfile).

The `media_gallery` directive accepts one inline option:

| Subdirective | Value | Default | Purpose |
|---|---|---|---|
| `template` | file name, relative to the templates dir | `gallery.tmpl` | Pick which template file to render. Path-traversal protected: no `..`, no absolute paths — the templates dir is a chroot. |
| `path_prefix` | URL prefix (e.g. `images`) | directory basename | URL mount prefix used in breadcrumb links. Defaults to the basename of the root directory if not set. |
| `root_name` | display name | `media root` | Display name for the root breadcrumb segment. |
| `image_types` | space-separated extensions (no leading dot) | `jpg jpeg png gif webp` | File extensions the gallery treats as images. Case-insensitive. **HEIC, AVIF, and SVG are NOT in the defaults** — Go's stdlib can't decode them. Files with those extensions are classified as "other" files (in the "Other files" section, not the image grid) and shown with a 📄 icon. Operators who want to handle these formats (e.g. via external tooling) can opt in with `image_types .heic .avif .svg`. |
| `video_types` | space-separated extensions (no leading dot) | `mp4 webm m4v mov mkv avi ogv ogg` | File extensions the gallery treats as videos. |
| `sort` | `mtime` / `name` | `mtime` | Sort field for the image grid. `mtime` = newest first; `name` = case-insensitive alphabetical. |
| `page_size` | integer &gt;= 1 | `60` (or first item in `page_sizes`) | Default per-page count. Deprecated: use `page_sizes` (list form) for the dropdown, which lets the visitor choose. |
| `page_sizes` | space-separated list (first = default) | `60 30 120 all` | Per-page dropdown options. The first item is the default. Use the `all` token to include "show everything on one page" in the dropdown. |
| `thumb_width` | integer &gt;= 1 | `320` | Max width (px) of generated thumbnails. |
| `thumb_height` | integer &gt;= 1 | `320` | Max height (px) of generated thumbnails. |
| `thumb_format` | `webp` / `png` / `jpeg` (or `jpg`) | `webp` | Output format for generated thumbnails. |
| `thumb_ttl` | integer (minutes) &gt;= 1 | `1440` (24h) | HTTP `Cache-Control: max-age` for thumb responses. |
| `cache_scan` | integer (minutes) &gt;= 1 | `1` | In-memory scan cache TTL. |
| `no_thumbs` | `true` / `false` (no-arg = `true`) | `false` (thumbs on) | Skip on-the-fly WebP thumbnail generation for **images**. Tile `<img src>` points to the original file instead of `~/_thumbs/<name>.webp`. Thumb requests fall through to the next handler. |
| `no_video_thumbs` | `true` / `false` (no-arg = `true`) | `false` (video thumbs on, if ffmpeg available) | Skip ffmpeg-based video poster extraction. |
| `no_exif` | `true` / `false` (no-arg = `true`) | `false` (EXIF on) | Disable EXIF entirely. EXIF is read LAZILY by the lightbox (via the `?exif=1` endpoint) — not at scan time. When `no_exif` is set, the endpoint returns 404 and the lightbox EXIF panel is hidden. Useful for privacy-sensitive deployments. Note that EXIF does NOT include GPS by default — see the EXIF section for details. |
| `search_match` | `word` / `substring` | `substring` | Filename match rule for the search feature. `word` = match the start of a word boundary (the original Phase 118 behaviour). `substring` = match anywhere in the filename. Both server-side and client-side filters use the same rule. |
| `max_cache_size_mb` | integer &gt;= 0 | `1024` (1 GB) | Cap on the on-disk thumb cache in MB. When the cache exceeds this, the oldest thumbs (by file mtime) are evicted until the cache is at 80% of the cap. Set to `0` to disable the cap entirely (unbounded — the pre-feature behavior). See [Caching & performance](#caching--performance) below for the full story. |

Example with a themed subdir:

```caddy
handle_path /images/* {
    root * /var/www/html/images
    media_gallery {
        template themes/dark/gallery.tmpl
    }
    file_server
}
```

This loads `$GALLERY_TEMPLATES_DIR/themes/dark/gallery.tmpl` (or
falls back to the bundled template if the file doesn't exist on
disk). The path is validated at Provision — an invalid name
(e.g. `../etc/passwd`) fails Caddy startup, not at first request.

### Local install (no root, no sudo)

For users without sudo access (shared host, locked-down
laptop), use `./build.sh --user`:

```bash
./build.sh --user           # default port 3245 (= 0xCAD, easter egg), serve ~/Pictures
./build.sh --user 9000      # custom port (must be > 1024)
CADDY_USER_ROOT=~/photos ./build.sh --user
```

This builds the binary to `~/bin/caddy` and generates
`Caddyfile.user` in the project root:

```caddy
{
    admin off
}

http://localhost:3245 {
    root * /home/user/Pictures

    handle_path /* {
        media_gallery
        file_server
    }
}
```

Then run:

```bash
~/bin/caddy run --config Caddyfile.user
# or backgrounded:
nohup ~/bin/caddy run --config Caddyfile.user > ~/caddy.log 2>&1 &
```

The `--user` mode enforces the port (>1024, so it never
needs root), uses the bundled template automatically
(no `GALLERY_TEMPLATES_DIR` needed), and leaves any
existing `Caddyfile.user` alone on subsequent builds.

The full argument matrix is in `./build.sh --help`.

### Example: skip thumbnail generation

```caddy
handle_path /images/* {
    root * /var/www/html/images
    media_gallery {
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
media_gallery {
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

### ffmpeg detection is startup-only — restart Caddy after installing it

`FFMPEG_PATH` and the `$PATH` lookup are evaluated exactly once, in the Caddy module's `Provision()` (the startup hook). The resolved path is cached on the `Gallery` struct and reused for every video thumbnail request thereafter. There is no per-request re-scan of `$PATH`, no per-request stat of the binary, and no hot-reload if ffmpeg is installed or upgraded while Caddy is running.

**If you install ffmpeg after Caddy is already running**, video thumbnails will continue to fail (404 from the thumb handler) until you restart Caddy. The restart re-runs `Provision()`, which re-evaluates `FFMPEG_PATH` and `$PATH` and re-caches the resolved path. The same applies if you change `FFMPEG_PATH` — it only takes effect on the next startup.

This is deliberate: the alternative (per-request detection) would mean inconsistent behavior during the transition window — newly-installed ffmpeg would work for some requests but not others, depending on timing — and would add an `exec.LookPath` syscall to every video thumb request for no real benefit.

To pick up a new or upgraded ffmpeg:

```bash
# System install
sudo systemctl restart caddy

# Local install (--user)
kill $(cat ~/caddy.pid) && ~/bin/caddy run --config Caddyfile.user
```

The Caddy startup log shows the resolved ffmpeg path (or a warning if it wasn't found) so you can verify the right binary was picked up — see the `Provision()` log output when Caddy starts.

Best for: small galleries (< 100 images) where you don't want a thumb cache and the originals aren't huge. Not recommended for large galleries — the page payload goes from ~30 KB (with thumbs) to ~5 MB average (full images) for a 1,000-image dir.

### Example: change the page size

```caddy
handle_path /images/* {
    root * /var/www/html/images
    media_gallery {
        page_size 100
    }
    file_server
}
```

This shows 100 image entries per page instead of the default 60. Tradeoffs: larger pages mean fewer HTTP requests, but each request returns a bigger HTML payload (and the server uses more memory per render). The pagination nav at the bottom of the page only renders when total pages > 1, so if your gallery has 30 images and you set `page_size 100`, you get all 30 on one page with no nav. (You can also use `page_sizes 100` to expose 100 in the dropdown and let the visitor switch back to 60.) URL query override: append `?page=2` to the gallery URL to jump to a specific page. ``?page_size=N` IS a valid URL query param: the visitor can switch the per-page size via the dropdown in the meta line. The value is validated against the operator-configured `page_sizes` list (an unknown value falls back to the first item). Changing the page size resets the visitor to page 1 (so they don't end up on a non-existent page).

All other configuration (the `root *` for the image directory,
the `handle` / `handle_path` for the route, the auth wrapper) is
via the surrounding Caddyfile block, not the `media_gallery`
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
  "handler": "media_gallery",
  "root": "/var/www/html/images"
}
```

The `Root` field is optional — the module falls back to the
per-request `root` set by the surrounding `handle` / `handle_path`
block. Set it explicitly only if you need to override the
request-time root.

### Full JSON config (all fields)

Here's a complete JSON config showing every configurable field
of the `media_gallery` handler, with realistic values:

```json
{
  "handler": "media_gallery",
  "root": "/var/www/html/images",
  "path_prefix": "images",
  "root_name": "images",
  "image_types": ["jpg", "jpeg", "png", "gif", "webp"],
  "video_types": ["mp4", "webm", "m4v", "mov", "mkv", "avi", "ogv", "ogg"],
  "sort": "name",
  "page_size": 60,
  "page_sizes": ["60", "30", "120", "all"],
  "thumb_width": 320,
  "thumb_height": 320,
  "thumb_format": "webp",
  "thumb_ttl": 1440,
  "cache_scan": 1,
  "no_thumbs": false,
  "no_video_thumbs": false,
  "template": "gallery.tmpl",
  "search_match": "substring",
  "max_cache_size_mb": 1024
}
```

All fields are optional except `handler` (always required).
Defaults match the Caddyfile defaults — if you omit a field,
the same default value applies.

### Caddyfile ↔ JSON field mapping

| Caddyfile directive | JSON field | Type | Default |
|---|---|---|---|
| `path_prefix <prefix>` | `"path_prefix"` | string | (directory basename) |
| `root_name <name>` | `"root_name"` | string | `media root` |
| `image_types <ext1 ext2 ...>` | `"image_types"` | `[]string` | built-in list |
| `video_types <ext1 ext2 ...>` | `"video_types"` | `[]string` | built-in list |
| `sort <mtime\|name>` | `"sort"` | string | `mtime` |
| `page_size <N>` | `"page_size"` | int | (first item of `page_sizes`) |
| `page_sizes <N1 N2 ...>` | `"page_sizes"` | `[]string` | `["60", "30", "120", "all"]` |
| `thumb_width <N>` | `"thumb_width"` | int | `320` |
| `thumb_height <N>` | `"thumb_height"` | int | `320` |
| `thumb_format <fmt>` | `"thumb_format"` | string | `webp` |
| `thumb_ttl <N>` | `"thumb_ttl"` | int (minutes) | `1440` |
| `cache_scan <N>` | `"cache_scan"` | int (minutes) | `1` |
| `no_thumbs` / `no_thumbs false` | `"no_thumbs"` | bool | `false` |
| `no_video_thumbs` / `no_video_thumbs false` | `"no_video_thumbs"` | bool | `false` |
| `template <name>` | `"template"` | string | `gallery.tmpl` |
| `search_match <word\|substring>` | `"search_match"` | string | `substring` |
| `max_cache_size_mb <N>` | `"max_cache_size_mb"` | int (MB) | `1024` (1 GB; `0` = no cap) |
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

## Caddy-level configuration (optional)

The `media_gallery` directive handles the rendering and
on-the-fly thumbnailing. A few performance knobs live at
the Caddy level (outside the module) and are recommended
for any production deployment:

### Compression

Caddy's built-in `encode` middleware compresses text
responses (HTML, CSS, JS, JSON, SVG) with gzip and/or
zstd. The gallery page is ~160 KB raw; gzip compresses it
to ~20 KB (an 8x reduction), so first-paint and time-to-
interactive drop significantly. Thumbnails are pre-
compressed image formats (WebP), so the encoder leaves
them alone.

```caddy
route {
    encode zstd gzip
    ...
}
```

`zstd` is preferred by modern browsers (slightly smaller
than gzip at similar decompression speed); `gzip` is the
universal fallback. Caddy auto-selects the best encoding
based on the `Accept-Encoding` header the browser sends.

The `encode` directive is built into Caddy core — no
extra `--with` flag is needed at build time.

### Static asset caching

`/favicon.ico`, theme CSS, and font files served by the
gallery can have long `Cache-Control` max-age values to
avoid re-downloading on every navigation:

```caddy
header /favicon.ico Cache-Control "public, max-age=86400"
```

For per-page assets (thumbnails), the module sets its own
TTL via the `thumb_ttl` subdirective (default 24h).

## What `media_gallery` does NOT do

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

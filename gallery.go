// Package gallery implements a media-gallery HTTP handler for Caddy v2.
// Supports images (jpg, png, gif, webp, avif, svg) and videos (mp4, webm,
// mov, mkv, etc.) with on-demand WebP thumbnails for images and
// ffmpeg-extracted first-frame thumbnails for videos. Replaces Caddy's
// default `file_server browse` with a richer directory listing that
// includes a lightbox, on-the-fly image thumbnailing, and video
// preview generation.
package gallery

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

func init() {
	caddy.RegisterModule(Gallery{})
	httpcaddyfile.RegisterHandlerDirective("media_gallery", parseCaddyfile)
	httpcaddyfile.RegisterDirectiveOrder("media_gallery", httpcaddyfile.Before, "file_server")
}

func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	g := new(Gallery)
	err := g.UnmarshalCaddyfile(h.Dispenser)
	return g, err
}

// Gallery is a Caddy HTTP handler that renders a directory as a
// dark-themed image/video gallery. See the README for behaviour.
type Gallery struct {
	// Root is the on-disk directory to render. Set automatically by
	// Caddy's `root` directive (via Provision), or can be set in JSON
	// config.
	Root string `json:"root,omitempty"`

	// PathPrefix is the URL mount prefix for the gallery
	// (e.g. "images" if the gallery is mounted at /images/*
	// in the Caddyfile). It is used by the breadcrumb so
	// the first segment matches what the user sees in the
	// URL. Defaults to filepath.Base(Root) if empty.
	PathPrefix string `json:"path_prefix,omitempty"`
	// RootName is the operator-configurable display name for
	// the first breadcrumb segment (the gallery's root). Set
	// in the Caddyfile via `root_name "My Gallery"`. If empty,
	// the resolved rootName (set in Provision) defaults to
	// "media root" — a generic name that works for any gallery.
	RootName string `json:"root_name,omitempty"`

	// pathPrefix is the resolved path prefix (after Provision
	// applies the default of filepath.Base(Root) if PathPrefix
	// is empty). This is what gets passed to RenderPage as the
	// breadcrumb's root label.
	pathPrefix string

	// rootName is the resolved display name for the first
	// breadcrumb segment (the gallery's root). Defaults to
	// "media root" if the operator didn't set RootName in
	// the Caddyfile (per user request 2026-06-20).
	rootName string

	// Sort is the field used to order the gallery. Valid values:
	//   "mtime" (default) — newest first
	//   "name"           — alphabetical
	Sort string `json:"sort,omitempty"`

	// Template is the name of the template file to use, relative to
	// the templates dir ($GALLERY_TEMPLATES_DIR, default
	// /etc/caddy/gallery-templates). If empty, defaults to
	// "gallery.tmpl". The path is validated at Provision to
	// reject absolute paths and any traversal above the templates
	// dir (no `..` allowed). The template dir is the chroot; the
	// operator can only reference files inside it.
	Template string `json:"template,omitempty"`

	// ImageExts is the set of file extensions the gallery treats
	// as images. Set from the `image_types` Caddyfile subdirective
	// (space-separated, case-insensitive, with or without leading
	// dot). If empty (the default), the plugin uses a built-in list
	// of common image extensions (jpg, jpeg, png, gif, webp, svg,
	// avif, heic). Provision() converts this list to a map for the
	// Scanner to use.
	ImageExts []string `json:"image_types,omitempty"`

	// VideoExts is the set of file extensions the gallery treats
	// as videos. Set from the `video_types` Caddyfile subdirective
	// (same syntax as image_types). Empty (default) uses the
	// built-in video list (mp4, webm, m4v, mov, mkv, avi, ogv, ogg).
	VideoExts []string `json:"video_types,omitempty"`

	// NoThumbs disables the on-the-fly WebP thumbnail generation.
	// When true, the gallery uses the original image as the tile
	// <img src> instead of `/_thumbs/<name>.webp`. Requests to the
	// thumb URL fall through to the next handler (typically
	// file_server, which 404s since no _thumbs/ dir exists).
	// Tradeoffs: no thumb cache, no CPU cost, but the browser
	// downloads the full image per tile (bigger page payload, slower
	// load on dirs of large photos). Useful for small galleries
	// where the operator doesn't want to maintain a thumb cache.
	NoThumbs bool `json:"no_thumbs,omitempty"`

	// NoVideoThumbs disables the on-demand WebP thumbnail generation
	// for VIDEO files (extracted from the first frame via ffmpeg).
	// When true, videos still display in the gallery (with the
	// placeholder gradient + play button on each tile) but no
	// per-frame thumbnail is produced or served. When false (the
	// default), video thumbnails ARE generated IF ffmpeg is
	// available on the host — if ffmpeg is missing, video thumbs
	// fall back to the placeholder regardless of this setting
	// (there's no way to generate a frame without a tool that can
	// decode the video).
	// Caddyfile: `no_video_thumbs` (no arg → true) or
	//            `no_video_thumbs false` (re-enable).
	NoVideoThumbs bool `json:"no_video_thumbs,omitempty"`

	// ffmpegPath is the absolute path to the ffmpeg binary, set
	// in Provision. Empty when ffmpeg is not installed (or when
	// NoVideoThumbs is true — we skip the lookup since it would
	// be unused). Resolution order:
	//   1. FFMPEG_PATH env var (if set + points to an executable)
	//   2. exec.LookPath("ffmpeg") (scans $PATH)
	// Thread-safe to read after Provision returns; written only
	// during Provision.
	ffmpegPath string `json:"-"`

	// imageExtsMap is the resolved image-extension set (after
	// lowercasing + dot-normalization in Provision) for fast
	// lookup in Scanner.Classify. Built from ImageExts (if
	// non-empty) or defaultImageExts. Set once in Provision;
	// read-only after that.
	imageExtsMap map[string]bool `json:"-"`

	// videoExtsMap is the resolved video-extension set, same
	// shape as imageExtsMap.
	videoExtsMap map[string]bool `json:"-"`

	// PageSize is the number of image entries per page. Default
	// is 50 (set in Provision if zero). The user can override
	// per-route via the Caddyfile: `media_gallery { page_size 100 }`.
	// Validation: must be > 0. A zero or negative value is rejected
	// by UnmarshalCaddyfile (the Caddyfile parser).
	PageSize int `json:"page_size,omitempty"`
	// PageSizes is the list of page sizes the visitor can
	// choose from in the per-page dropdown (e.g. [30, 60, 120,
	// "all"]). Set in the Caddyfile via `page_sizes 30 60 120 all`
	// (space-separated; "all" as a token means "show all items
	// in one page" - only included if explicitly listed).
	// If empty (default), the resolved PageSizes (set in
	// Provision) is [30, 60, 120, "all"]. The CURRENT page size
	// is set via the URL ?page_size=N parameter, which is
	// validated against this list (unknown values fall back to
	// the first item).
	PageSizes []string `json:"page_sizes,omitempty"`

	// ThumbWidth is the maximum width in pixels of generated
	// thumbnails. The source image is fit-within-bounds (aspect
	// ratio preserved, longest edge becomes the configured value).
	// Default: 320. Caddyfile: `thumb_width 480`.
	// Validation: must be > 0; zero/negative rejected.
	ThumbWidth int `json:"thumb_width,omitempty"`

	// ThumbHeight is the maximum height in pixels of generated
	// thumbnails. Fit-within-bounds behavior — see ThumbWidth.
	// Default: 320. Caddyfile: `thumb_height 480`.
	// Validation: must be > 0.
	ThumbHeight int `json:"thumb_height,omitempty"`

	// ThumbFormat is the output format for generated thumbnails.
	// One of: "jpeg" (or "jpg"), "png", or "webp" (the current
	// default, lossless). Default: "webp". Caddyfile:
	// `thumb_format jpeg`. Validation: must be one of the three.
	ThumbFormat string `json:"thumb_format,omitempty"`

	// CacheScanMinutes is the in-memory scan cache TTL in
	// minutes. Default: 1. Caddyfile: `cache_scan 5`.
	// Validation: must be > 0.
	CacheScanMinutes int `json:"cache_scan,omitempty"`

	// ThumbTTLMinutes is the HTTP Cache-Control max-age in
	// minutes for thumb responses. Thumbs are immutable per
	// source mtime, so a long TTL is safe. Default: 1440
	// (= 24 hours, matches the previous 86400-second value).
	// Caddyfile: `thumb_ttl 60`. Validation: must be > 0.
	ThumbTTLMinutes int `json:"thumb_ttl,omitempty"`

	// Cache holds the in-memory scan cache. Initialised in Provision
	// if nil. Excluded from JSON config (runtime state only).
	Cache *ScanCache `json:"-"`
}

func (Gallery) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.media_gallery",
		New: func() caddy.Module { return new(Gallery) },
	}
}

// Provision sets up the module. Creates a default scan cache if one
// isn't already set.
func (g *Gallery) Provision(caddy.Context) error {
	if g.Cache == nil {
		g.Cache = NewScanCache(time.Duration(g.CacheScanMinutes) * time.Minute)
	}
	// Validate the configured template name. Must be relative and
	// must not traverse above the templates dir. Fail Caddy
	// startup on a bad value so a misconfiguration is caught at
	// boot, not at first request.
	if _, err := sanitizeTemplateName(g.Template); err != nil {
		return fmt.Errorf("invalid media_gallery template name %q: %w", g.Template, err)
	}
	// Apply defaults for the Caddyfile-only fields. Zero or
	// empty means "use the default" — the UnmarshalCaddyfile
	// already rejects invalid values, so if we see zero/empty
	// here it just means the user didn't specify a value.
	if g.PageSize == 0 {
		g.PageSize = 50
	}
	if g.ThumbWidth == 0 {
		g.ThumbWidth = 320
	}
	if g.ThumbHeight == 0 {
		g.ThumbHeight = 320
	}
	if g.ThumbFormat == "" {
		g.ThumbFormat = "webp"
	}
	if g.CacheScanMinutes == 0 {
		g.CacheScanMinutes = 1
	}
	if g.ThumbTTLMinutes == 0 {
		g.ThumbTTLMinutes = 1440 // 24 hours, matches the previous 86400s
	}
	// Resolve the page sizes list (default [30, 60, 120, "all"]).
	// The operator can override via the `page_sizes` Caddyfile
	// directive (space-separated list). If they include "all"
	// it means "show all items on one page" - only included if
	// explicitly listed.
	if len(g.PageSizes) == 0 {
		g.PageSizes = []string{"30", "60", "120", "all"}
	}
	// Resolve the image + video extension sets. If the operator
	// configured them via the Caddyfile, use their list;
	// otherwise fall back to the built-in defaults. The resolved
	// maps are passed to ScanCache.Get and used by Scanner.Classify.
	// Resolve the path prefix. If PathPrefix is set in the
	// Caddyfile, use it; otherwise default to the basename
	// of the gallery's root directory. For example, if the
	// Caddyfile mounts the gallery at /images/* with root
	// /var/www/html/images, the default pathPrefix is "images".
	// This is what gets passed to RenderPage as the breadcrumb's
	// root label.
	g.pathPrefix = g.PathPrefix
	if g.pathPrefix == "" {
		g.pathPrefix = filepath.Base(g.Root)
	}
	// Resolve the breadcrumb root name. If the operator
	// configured `root_name` in the Caddyfile, use it;
	// otherwise default to the basename of the gallery's
	// root directory (same default as the path prefix).
	g.rootName = g.RootName
	if g.rootName == "" {
		g.rootName = filepath.Base(g.Root)
	}

	g.imageExtsMap = defaultImageExts
	if len(g.ImageExts) > 0 {
		g.imageExtsMap = extsToMap(g.ImageExts)
	}
	g.videoExtsMap = defaultVideoExts
	if len(g.VideoExts) > 0 {
		g.videoExtsMap = extsToMap(g.VideoExts)
	}
	// Detect ffmpeg for video thumbnail generation. We do this
	// once at Provision (not per-scan) since ffmpeg availability
	// doesn't change at runtime. If ffmpeg is missing OR
	// NoVideoThumbs is true, g.ffmpegPath stays empty and the
	// video-thumb code path falls back to the placeholder.
	//
	// Resolution order (Phase 67):
	//   1. FFMPEG_PATH env var (if set and points to an executable)
	//   2. exec.LookPath("ffmpeg") (scans $PATH)
	// If both fail, g.ffmpegPath stays empty and the video-thumb
	// code path falls back to the placeholder.
	if !g.NoVideoThumbs {
		if path := os.Getenv("FFMPEG_PATH"); path != "" {
			// Verify the env var actually points to an executable.
			// (We don't want to silently store a bad path that
			// would fail at request time with a confusing error.)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				if info.Mode()&0o111 != 0 {
					g.ffmpegPath = path
				}
			}
		}
		if g.ffmpegPath == "" {
			if path, err := exec.LookPath("ffmpeg"); err == nil {
				g.ffmpegPath = path
			}
		}
		// Per Phase 106: log the resolved ffmpeg path so the operator
		// can confirm the right binary was picked up at startup.
		// (The path is cached and reused for every video thumb request
		// thereafter — see docs/01-configuration.md for the
		// restart-after-install rationale.)
		if g.ffmpegPath != "" {
			fmt.Fprintf(os.Stderr, "caddy-media-gallery: ffmpeg path: %s\n", g.ffmpegPath)
		} else if !g.NoVideoThumbs {
			fmt.Fprintf(os.Stderr, "caddy-media-gallery: ffmpeg NOT FOUND (video thumbnails disabled; set FFMPEG_PATH or install ffmpeg and restart Caddy)\n")
		}
	}
	// Make the bundled templates discoverable on disk for the
	// operator. writeBundledTemplates is a no-op if the files
	// already exist (operator overrides preserved), and a
	// non-fatal error here doesn't block the module from serving
	// (the bundled templates still work).
	if err := writeBundledTemplates(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: caddy-media-gallery: could not write bundled templates to disk: %v\n", err)
	}
	return nil
}

func (*Gallery) Cleanup()        {}
func (*Gallery) Validate() error { return nil }

// ServeHTTP renders the gallery for the directory at the current
// request path, or falls through to the next handler (typically
// file_server) if the request is for a file.
//
// Path semantics after handle_path /images/* strips the prefix:
//
//	r.URL.Path = ""                    → render gallery for root
//	r.URL.Path = "subdir"              → render gallery for subdir
//	r.URL.Path = "subdir/"             → render gallery for subdir
//	r.URL.Path = "photo.jpg"           → fall through to file_server
//	r.URL.Path = "subdir/photo.jpg"    → fall through to file_server
//	r.URL.Path = "_thumbs/photo.webp"  → serve as thumbnail
//	r.URL.Path = "subdir/_thumbs/x.webp" → serve as thumbnail in subdir
//
// On transient errors (scan failures, template parse), it falls
// through to the next handler rather than returning a 500.
func (g *Gallery) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	root := g.Root
	if root == "" {
		if v, ok := caddyhttp.GetVar(r.Context(), "root").(string); ok && v != "" {
			root = v
		}
	}
	if root == "" {
		http.Error(w, "media_gallery: no root configured (set Gallery.Root in JSON or use `root * /path` in the Caddyfile)", http.StatusInternalServerError)
		return nil
	}
	// Normalise the path. r.URL.Path may or may not have a leading
	// slash depending on Caddy's handle_path internals; we strip it
	// so filepath.Join behaves correctly and the resulting path is
	// relative to the gallery root.
	relPath := strings.TrimPrefix(r.URL.Path, "/")
	resolved := filepath.Join(root, relPath)

	// Thumb requests get a special handler. It resolves the source
	// file in (root + subdir) for the path BEFORE the _thumbs/
	// segment, so /subdir/_thumbs/photo.webp looks up
	// (root/subdir/photo.<ext>).
	//
	// When no_thumbs is enabled, we skip this branch entirely.
	// The request falls through to the next handler (file_server
	// or whatever) which 404s because no _thumbs/ dir exists on
	// disk. This is the correct behavior — the thumb URL just
	// doesn't exist for galleries that don't generate thumbs.
	if !g.NoThumbs {
		if g.serveThumb(w, r, root, relPath) {
			return nil
		}
	}

	// If the resolved path exists and is a regular file, fall
	// through to file_server (or whatever the next handler is).
	if info, err := os.Stat(resolved); err == nil && !info.IsDir() {
		return next.ServeHTTP(w, r)
	}
	// If the path doesn't exist at all, fall through so file_server
	// returns a real 404 (not a 200 with an empty-gallery page).
	if _, err := os.Stat(resolved); err != nil {
		return next.ServeHTTP(w, r)
	}

	// Path is a directory. If the request URL doesn't end with a
	// trailing slash, 301-redirect to the canonical form so the
	// browser resolves relative URLs (./_thumbs/photo.webp) the
	// same way it does for the trailing-slash version. This matches
	// what file_server does for directory indexes; without it,
	// visiting /images/foo resolves ./ against /images/ instead of
	// /images/foo/, breaking the thumb URLs.
	//
	// We use a RELATIVE Location (no leading slash) because
	// Caddy's handle_path rewrites both r.URL.Path AND
	// r.RequestURI, so we can't reconstruct the full original
	// URL from inside the handler. A relative reference is
	// resolved by the browser against the current request URL
	// per RFC 3986 §5.2 — "generated/" against base
	// "/images/generated" yields "/images/generated/" via the
	// merge algorithm, regardless of whether the browser
	// treats the base as a file or a directory.
	//
	// We set the Location header manually instead of using
	// http.Redirect() because the latter normalises the
	// location to an absolute path (prepending "/"), which the
	// browser would then resolve against the host root instead
	// of the request URL.
	if relPath != "" && !strings.HasSuffix(relPath, "/") {
		w.Header().Set("Location", relPath+"/")
		w.WriteHeader(http.StatusMovedPermanently)
		return nil
	}

	// It's a directory. Scan it and render the gallery.
	files, err := g.Cache.Get(resolved, g.Sort, g.imageExtsMap, g.videoExtsMap)
	if err != nil {
		// Scan failure (permission denied, etc.) — fall through.
		return next.ServeHTTP(w, r)
	}
	// Title: basename of the resolved dir, falling back to the
	// gallery root for the top-level case.
	title := filepath.Base(resolved)
	if title == "." || title == "" {
		title = filepath.Base(root)
	}
	body, err := RenderPage(title, "./", "./_thumbs/", relPath, g.Template, g.NoThumbs, g.NoVideoThumbs, g.PageSize, g.PageSizes, files, r.URL.Query(), g.imageExtsMap, g.videoExtsMap, g.rootName, g.PathPrefix)
	if err != nil {
		http.Error(w, "media_gallery: render failed: "+err.Error(), http.StatusInternalServerError)
		return nil
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(body))
	return nil
}

func (g *Gallery) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	g.Sort = "mtime"
	for d.Next() {
		for d.NextBlock(0) {
			switch d.Val() {
			case "sort":
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.Sort = d.Val()
				if g.Sort != "mtime" && g.Sort != "name" {
					return d.ArgErr()
				}
			case "template":
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.Template = d.Val()
				if d.NextArg() {
					return d.ArgErr()
				}
			case "no_thumbs":
				g.NoThumbs = true
				if d.NextArg() {
					if d.Val() != "false" {
						return d.ArgErr()
					}
					g.NoThumbs = false
				}
			case "no_video_thumbs":
				g.NoVideoThumbs = true
				if d.NextArg() {
					if d.Val() != "false" {
						return d.ArgErr()
					}
					g.NoVideoThumbs = false
				}
			case "page_size":
				if !d.NextArg() {
					return d.ArgErr()
				}
				n, err := strconv.Atoi(d.Val())
				if err != nil {
					return d.ArgErr()
				}
				if n <= 0 {
					return d.ArgErr()
				}
				g.PageSize = n
				if d.NextArg() {
					return d.ArgErr()
				}
			case "page_sizes":
				// Space-separated list of page size options the
				// visitor can choose from in the per-page dropdown.
				// Special token "all" means "show all items in
				// one page" (only included if explicitly listed).
				// Examples:
				//   page_sizes 30 60 120 all
				//   page_sizes 50 100
				//   page_sizes 25 50 75 100
				// If the operator doesn't set page_sizes, the
				// default is [30, 60, 120, "all"] (per user
				// request 2026-06-20).
				g.PageSizes = nil
				for d.NextArg() {
					g.PageSizes = append(g.PageSizes, d.Val())
				}
				if len(g.PageSizes) == 0 {
					return d.ArgErr()
				}
			case "thumb_width":
				if !d.NextArg() {
					return d.ArgErr()
				}
				n, err := strconv.Atoi(d.Val())
				if err != nil || n <= 0 {
					return d.ArgErr()
				}
				g.ThumbWidth = n
				if d.NextArg() {
					return d.ArgErr()
				}
			case "thumb_height":
				if !d.NextArg() {
					return d.ArgErr()
				}
				n, err := strconv.Atoi(d.Val())
				if err != nil || n <= 0 {
					return d.ArgErr()
				}
				g.ThumbHeight = n
				if d.NextArg() {
					return d.ArgErr()
				}
			case "thumb_format":
				if !d.NextArg() {
					return d.ArgErr()
				}
				format := d.Val()
				if format != "jpeg" && format != "jpg" && format != "png" && format != "webp" {
					return d.ArgErr()
				}
				g.ThumbFormat = format
				if d.NextArg() {
					return d.ArgErr()
				}
			case "cache_scan":
				if !d.NextArg() {
					return d.ArgErr()
				}
				n, err := strconv.Atoi(d.Val())
				if err != nil || n <= 0 {
					return d.ArgErr()
				}
				g.CacheScanMinutes = n
				if d.NextArg() {
					return d.ArgErr()
				}
			case "thumb_ttl":
				if !d.NextArg() {
					return d.ArgErr()
				}
				n, err := strconv.Atoi(d.Val())
				if err != nil || n <= 0 {
					return d.ArgErr()
				}
				g.ThumbTTLMinutes = n
				if d.NextArg() {
					return d.ArgErr()
				}
			case "image_types":
				// Space-separated list of image extensions. Empty
				// args are silently skipped; entries are normalized
				// to ".ext" + lowercase by extsToMap() in Provision.
				// Examples:
				//   image_types jpg jpeg png
				//   image_types .jpg .png .heic   (leading dot allowed)
				//   image_types JPG JPEG PNG      (case-insensitive)
				g.ImageExts = nil
				for d.NextArg() {
					g.ImageExts = append(g.ImageExts, d.Val())
				}
			case "video_types":
				// Same shape as image_types. Empty args skipped.
				//   video_types mp4 webm mov
				g.VideoExts = nil
				for d.NextArg() {
					g.VideoExts = append(g.VideoExts, d.Val())
				}
			case "path_prefix":
				// URL mount path for the gallery, used by the
				// breadcrumb to build absolute links. Example:
				// if the gallery is mounted at /images/* in the
				// Caddyfile, set
				//   path_prefix /images/
				// so the breadcrumb's first segment is "images"
				// (links to "/images/") and subsequent segments
				// are absolute URLs like "/images/media_gallery/".
				// If empty (default), the breadcrumb uses relative
				// links (./seg/...) — backwards-compatible.
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.PathPrefix = d.Val()
				if d.NextArg() {
					return d.ArgErr()
				}
			case "root_name":
				// Display name for the first breadcrumb
				// segment (the gallery's root). Set in the
				// Caddyfile as e.g. `root_name images`. Used
				// by the breadcrumb to show a custom name
				// (like "images", "Photos", "My Gallery")
				// instead of the default "media root".
				if !d.NextArg() {
					return d.ArgErr()
				}
				g.RootName = d.Val()
				if d.NextArg() {
					return d.ArgErr()
				}
			}
		}
	}
	return nil
}

// Compile-time interface compliance
var (
	_ caddyfile.Unmarshaler       = (*Gallery)(nil)
	_ caddyhttp.MiddlewareHandler = (*Gallery)(nil)
)

// Package gallery implements an image-gallery HTTP handler for Caddy v2.
// It renders a directory as a dark-themed grid of thumbnails with a vanilla
// JS lightbox for click-to-expand.
package gallery

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

func init() {
	caddy.RegisterModule(Gallery{})
	httpcaddyfile.RegisterHandlerDirective("image_gallery", parseCaddyfile)
	httpcaddyfile.RegisterDirectiveOrder("image_gallery", httpcaddyfile.Before, "file_server")
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

	// Cache holds the in-memory scan cache. Initialised in Provision
	// if nil. Excluded from JSON config (runtime state only).
	Cache *ScanCache `json:"-"`
}

func (Gallery) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.image_gallery",
		New: func() caddy.Module { return new(Gallery) },
	}
}

// Provision sets up the module. Creates a default scan cache if one
// isn't already set.
func (g *Gallery) Provision(caddy.Context) error {
	if g.Cache == nil {
		g.Cache = NewScanCache(time.Minute)
	}
	// Validate the configured template name. Must be relative and
	// must not traverse above the templates dir. Fail Caddy
	// startup on a bad value so a misconfiguration is caught at
	// boot, not at first request.
	if _, err := sanitizeTemplateName(g.Template); err != nil {
		return fmt.Errorf("invalid image_gallery template name %q: %w", g.Template, err)
	}
	// Make the bundled templates discoverable on disk for the
	// operator. writeBundledTemplates is a no-op if the files
	// already exist (operator overrides preserved), and a
	// non-fatal error here doesn't block the module from serving
	// (the bundled templates still work).
	if err := writeBundledTemplates(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: caddy-image-gallery: could not write bundled templates to disk: %v\n", err)
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
		http.Error(w, "image_gallery: no root configured (set Gallery.Root in JSON or use `root * /path` in the Caddyfile)", http.StatusInternalServerError)
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
	if g.serveThumb(w, r, root, relPath) {
		return nil
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
	files, err := g.Cache.Get(resolved, g.Sort)
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
	body, err := RenderPage(title, "./", "./_thumbs/", relPath, g.Template, files, r.URL.Query())
	if err != nil {
		http.Error(w, "image_gallery: render failed: "+err.Error(), http.StatusInternalServerError)
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

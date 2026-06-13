// Package gallery implements an image-gallery HTTP handler for Caddy v2.
// It renders a directory as a dark-themed grid of thumbnails with a vanilla
// JS lightbox for click-to-expand.
package gallery

import (
	"net/http"
	"path/filepath"
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
	return nil
}

func (*Gallery) Cleanup()        {}
func (*Gallery) Validate() error { return nil }

// ServeHTTP renders the gallery for the configured root directory.
// On transient errors (scan failures, template parse), it falls
// through to the next handler rather than returning a 500.
func (g *Gallery) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	// Thumb requests are handled inline before the root check.
	// Caddy's handle_path strips the route prefix, so r.URL.Path is
	// like "_thumbs/photo.webp" (no leading slash) for a request
	// to /images/_thumbs/photo.webp on route "handle_path /images/* { ... }".
	if g.serveThumb(w, r) {
		return nil
	}
	if g.Root == "" {
		http.Error(w, "image_gallery: no root configured", http.StatusInternalServerError)
		return nil
	}
	files, err := g.Cache.Get(g.Root, g.Sort)
	if err != nil {
		// Fall through to the next handler on scan failure so the
		// gallery doesn't 500 on transient I/O issues.
		return next.ServeHTTP(w, r)
	}
	title := filepath.Base(g.Root)
	body, err := RenderPage(title, "./", "./_thumbs/", files)
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

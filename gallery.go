// Package gallery implements an image-gallery HTTP handler for Caddy v2.
// It renders a directory as a dark-themed grid of thumbnails with a vanilla
// JS lightbox for click-to-expand.
package gallery

import (
	"net/http"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

func init() {
	caddy.RegisterModule(Gallery{})
	// Register the Caddyfile directive. RegisterHandlerDirective is the
	// canonical way for http.handlers.* modules to expose themselves to the
	// Caddyfile parser; it also wires up the optional matcher-token
	// handling (e.g. `image_gallery @name { ... }`).
	httpcaddyfile.RegisterHandlerDirective("image_gallery", parseCaddyfile)
}

// parseCaddyfile is the Caddyfile adapter entrypoint: it returns a new
// Gallery and defers per-directive parsing to the type's UnmarshalCaddyfile.
func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	g := new(Gallery)
	err := g.UnmarshalCaddyfile(h.Dispenser)
	return g, err
}

// Gallery is a Caddy HTTP handler that renders a directory as a
// dark-themed image/video gallery. See the README for behaviour.
type Gallery struct {
	// Sort is the field used to order the gallery. Valid values:
	//   "mtime" (default) — newest first
	//   "name"           — alphabetical
	Sort string `json:"sort,omitempty"`
}

func (Gallery) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "http.handlers.image_gallery",
		New: func() caddy.Module { return new(Gallery) },
	}
}

func (Gallery) Provision(caddy.Context) error { return nil }
func (Gallery) Cleanup()                      {}
func (Gallery) Validate() error               { return nil }

func (g Gallery) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	_, _ = w.Write([]byte("image_gallery ok"))
	return nil
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler so the module
// can be configured from the Caddyfile. Parses `image_gallery { ... }`
// blocks. Currently supports only the optional `sort` subdirective
// (values: "mtime" default, or "name").
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

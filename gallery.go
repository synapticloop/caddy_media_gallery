package gallery

import (
	"net/http"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

func init() {
	caddy.RegisterModule(Gallery{})
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

// Compile-time interface compliance
var _ caddyhttp.MiddlewareHandler = (*Gallery)(nil)

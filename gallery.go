package gallery

import (
	"net/http"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

func init() {
	caddy.RegisterModule(Gallery{})
}

type Gallery struct{}

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

package gallery

import (
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
)

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

// Compile-time checks
var (
	_ caddyfile.Unmarshaler       = (*Gallery)(nil)
	_ caddyhttp.MiddlewareHandler = (*Gallery)(nil)
)

package gallery

import (
	"testing"

	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

// TestSanitizeTemplateName covers the path-traversal protection on
// the configured template name. The name must be a relative path
// inside the templates dir; absolute paths and any ".." traversal
// are rejected. Empty is allowed (means "use the default").
func TestSanitizeTemplateName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string // cleaned name, empty if not provided
		wantErr bool
	}{
		// Allowed
		{"empty = use default", "", "", false},
		{"simple filename", "gallery.tmpl", "gallery.tmpl", false},
		{"subdir", "themes/dark/gallery.tmpl", "themes/dark/gallery.tmpl", false},
		{"redundant slashes", "themes//dark/gallery.tmpl", "themes/dark/gallery.tmpl", false},
		{"dot path", "./gallery.tmpl", "gallery.tmpl", false},
		// Rejected: absolute
		{"absolute unix", "/etc/passwd", "", true},
		{"absolute template path", "/etc/caddy/other/gallery.tmpl", "", true},
		// Rejected: traversal
		{"traversal simple", "..", "", true},
		{"traversal with subdir", "../etc/passwd", "", true},
		{"traversal nested", "../../etc/passwd", "", true},
		{"traversal in middle", "themes/../../../etc/passwd", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := sanitizeTemplateName(c.input)
			if c.wantErr {
				if err == nil {
					t.Errorf("sanitizeTemplateName(%q): expected error, got %q", c.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("sanitizeTemplateName(%q): unexpected error: %v", c.input, err)
				return
			}
			if got != c.want {
				t.Errorf("sanitizeTemplateName(%q): got %q, want %q", c.input, got, c.want)
			}
		})
	}
}

// TestUnmarshalCaddyfile_TemplateDirective covers the new
// `template <name>` Caddyfile directive.
func TestUnmarshalCaddyfile_TemplateDirective(t *testing.T) {
	t.Run("no template directive → preserved", func(t *testing.T) {
		// A caddyfile without the `template` directive must not
		// touch a pre-existing Gallery.Template — e.g. one set
		// via JSON config. This is the no-directive round-trip.
		g := Gallery{Template: "preserved.tmpl"}
		d := caddyfile.NewTestDispenser("image_gallery { sort name }")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.Template != "preserved.tmpl" {
			t.Errorf("expected Template preserved at %q, got %q", "preserved.tmpl", g.Template)
		}
	})
	t.Run("template gallery.tmpl", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n	template gallery.tmpl\n}\n")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.Template != "gallery.tmpl" {
			t.Errorf("expected Template=gallery.tmpl, got %q", g.Template)
		}
	})
	t.Run("template themes/dark/index.tmpl", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n	template themes/dark/index.tmpl\n}\n")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.Template != "themes/dark/index.tmpl" {
			t.Errorf("expected Template=themes/dark/index.tmpl, got %q", g.Template)
		}
	})
	t.Run("template with too many args → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n	template a.tmpl b.tmpl\n}\n")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for too many args to template directive")
		}
	})
}

func TestModuleRegistered(t *testing.T) {
	// Smoke test: the init() above registers the module. We can verify
	// the type is constructable and the ModuleInfo is correct.
	var g Gallery
	info := g.CaddyModule()
	if info.ID != "http.handlers.image_gallery" {
		t.Errorf("expected module ID %q, got %q", "http.handlers.image_gallery", info.ID)
	}
	if info.New == nil {
		t.Error("expected New constructor to be non-nil")
	}
}

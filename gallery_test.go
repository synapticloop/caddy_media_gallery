package gallery

import (
	"strings"
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

// TestUnmarshalCaddyfile_NoThumbs covers the new
// `no_thumbs` Caddyfile directive. Accepts:
//   - `no_thumbs` (no arg) → true
//   - `no_thumbs false`    → false
//   - anything else → error
func TestUnmarshalCaddyfile_NoThumbs(t *testing.T) {
	t.Run("no_thumbs (no arg) → true", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  no_thumbs\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if !g.NoThumbs {
			t.Error("expected NoThumbs=true after `no_thumbs` directive")
		}
	})
	t.Run("no_thumbs false → false", func(t *testing.T) {
		g := Gallery{NoThumbs: true}
		d := caddyfile.NewTestDispenser("image_gallery { no_thumbs false }")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.NoThumbs {
			t.Error("expected NoThumbs=false after `no_thumbs false` directive")
		}
	})
	t.Run("no_thumbs with bogus arg → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery { no_thumbs off }")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for `no_thumbs off` (must be `false`, not `off`)")
		}
	})
	t.Run("no_thumbs + template both set", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  template themes/dark/gallery.tmpl\n  no_thumbs\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.Template != "themes/dark/gallery.tmpl" {
			t.Errorf("Template: got %q, want %q", g.Template, "themes/dark/gallery.tmpl")
		}
		if !g.NoThumbs {
			t.Error("expected NoThumbs=true")
		}
	})
}

// TestRenderPage_NoThumbs_OriginalImageAsThumb verifies that
// when no_thumbs is true, the rendered tile <img> uses the
// original file URL (the Href) as its src, not the thumb URL.
func TestRenderPage_NoThumbs_OriginalImageAsThumb(t *testing.T) {
	files := []FileInfo{
		{Name: "photo.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	html, err := RenderPage(
		"test", "./", "./_thumbs/", "", "", true, files, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `src="./photo.jpg"`) {
		t.Errorf("expected src=./photo.jpg in no-thumbs output, got:\n%s",
			extractImgSrcs(html))
	}
	if strings.Contains(html, `src="./_thumbs/photo.webp"`) {
		t.Errorf("did NOT expect thumb URL in no-thumbs output, got:\n%s",
			extractImgSrcs(html))
	}
}

// TestRenderPage_WithThumbs_ThumbURLUsed verifies the default
// (thumbs enabled) behavior: the tile <img src> is the thumb URL.
func TestRenderPage_WithThumbs_ThumbURLUsed(t *testing.T) {
	files := []FileInfo{
		{Name: "photo.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	html, err := RenderPage(
		"test", "./", "./_thumbs/", "", "", false, files, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `src="./_thumbs/photo.webp"`) {
		t.Errorf("expected src=./_thumbs/photo.webp in default output, got:\n%s",
			extractImgSrcs(html))
	}
}

// extractImgSrcs is a small helper that pulls every <img src="...">
// out of an HTML string, one per line. Used in failure messages
// so the test output shows the actual srcs instead of the whole page.
func extractImgSrcs(html string) string {
	var out []string
	for _, line := range strings.Split(html, "\n") {
		if idx := strings.Index(line, `src="`); idx >= 0 {
			rest := line[idx+len(`src="`):]
			if end := strings.Index(rest, `"`); end >= 0 {
				out = append(out, "  "+rest[:end])
			}
		}
	}
	return strings.Join(out, "\n")
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

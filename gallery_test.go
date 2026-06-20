package gallery

import (
	"strings"
	"testing"

	"github.com/caddyserver/caddy/v2"
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

// TestUnmarshalCaddyfile_NoVideoThumbs verifies the new
// no_video_thumbs directive (Phase 62):
//   - `no_video_thumbs` (no arg) → NoVideoThumbs = true
//   - `no_video_thumbs false`     → NoVideoThumbs = false
//   - anything else → error
//
// Same pattern as no_thumbs, but for the video thumbnail
// generator (which uses ffmpeg to extract the first frame).
func TestUnmarshalCaddyfile_NoVideoThumbs(t *testing.T) {
	t.Run("no_video_thumbs (no arg) → true", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  no_video_thumbs\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if !g.NoVideoThumbs {
			t.Error("expected NoVideoThumbs=true after `no_video_thumbs` directive")
		}
	})
	t.Run("no_video_thumbs false → false", func(t *testing.T) {
		g := Gallery{NoVideoThumbs: true}
		d := caddyfile.NewTestDispenser("image_gallery { no_video_thumbs false }")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.NoVideoThumbs {
			t.Error("expected NoVideoThumbs=false after `no_video_thumbs false` directive")
		}
	})
	t.Run("no_video_thumbs with bogus arg → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery { no_video_thumbs off }")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for `no_video_thumbs off` (must be `false`, not `off`)")
		}
	})
	t.Run("no_video_thumbs + no_thumbs together", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  no_thumbs\n  no_video_thumbs\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if !g.NoThumbs || !g.NoVideoThumbs {
			t.Error("expected both NoThumbs and NoVideoThumbs to be true")
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
		"test", "./", "./_thumbs/", "", "", true, false, 0, files, nil,
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
		"test", "./", "./_thumbs/", "", "", false, false, 0, files, nil,
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
// TestUnmarshalCaddyfile_PageSize covers the new
// `page_size <int>` Caddyfile directive. Accepts positive
// integers; rejects zero, negative, and non-numeric values.
func TestUnmarshalCaddyfile_PageSize(t *testing.T) {
	t.Run("page_size 100", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  page_size 100\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.PageSize != 100 {
			t.Errorf("PageSize: got %d, want 100", g.PageSize)
		}
	})
	t.Run("page_size 1 (minimum valid)", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  page_size 1\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.PageSize != 1 {
			t.Errorf("PageSize: got %d, want 1", g.PageSize)
		}
	})
	t.Run("page_size 0 → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  page_size 0\n}")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for `page_size 0` (must be > 0)")
		}
	})
	t.Run("page_size -5 → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  page_size -5\n}")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for `page_size -5` (must be > 0)")
		}
	})
	t.Run("page_size abc → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  page_size abc\n}")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for `page_size abc` (must be an integer)")
		}
	})
	t.Run("page_size with no arg → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  page_size\n}")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for `page_size` with no value")
		}
	})
}

// TestProvision_PageSizeDefault verifies that Provision applies
// the default of 50 when the Caddyfile doesn't set page_size.
func TestProvision_PageSizeDefault(t *testing.T) {
	g := Gallery{}
	if err := g.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	if g.PageSize != 50 {
		t.Errorf("expected default PageSize=50 after Provision, got %d", g.PageSize)
	}
}

// TestProvision_PageSizePreserved verifies that Provision doesn't
// override an explicit page_size set via the Caddyfile.
func TestProvision_PageSizePreserved(t *testing.T) {
	g := Gallery{PageSize: 100}
	if err := g.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	if g.PageSize != 100 {
		t.Errorf("expected PageSize=100 preserved, got %d", g.PageSize)
	}
}

// TestUnmarshalCaddyfile_ThumbConfig covers the 5 new Caddyfile
// directives: thumb_width, thumb_height, thumb_format, cache_scan,
// thumb_ttl. All accept a positive int (or a string for
// thumb_format), with validation that rejects invalid values.
func TestUnmarshalCaddyfile_ThumbConfig(t *testing.T) {
	t.Run("thumb_width 480", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  thumb_width 480\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.ThumbWidth != 480 {
			t.Errorf("ThumbWidth: got %d, want 480", g.ThumbWidth)
		}
	})
	t.Run("thumb_width 0 → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  thumb_width 0\n}")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for thumb_width 0")
		}
	})
	t.Run("thumb_width -1 → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  thumb_width -1\n}")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for thumb_width -1")
		}
	})
	t.Run("thumb_height 240", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  thumb_height 240\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.ThumbHeight != 240 {
			t.Errorf("ThumbHeight: got %d, want 240", g.ThumbHeight)
		}
	})
	t.Run("thumb_format jpeg", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  thumb_format jpeg\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.ThumbFormat != "jpeg" {
			t.Errorf("ThumbFormat: got %q, want jpeg", g.ThumbFormat)
		}
	})
	t.Run("thumb_format jpg (alias)", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  thumb_format jpg\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.ThumbFormat != "jpg" {
			t.Errorf("ThumbFormat: got %q, want jpg", g.ThumbFormat)
		}
	})
	t.Run("thumb_format png", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  thumb_format png\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.ThumbFormat != "png" {
			t.Errorf("ThumbFormat: got %q, want png", g.ThumbFormat)
		}
	})
	t.Run("thumb_format webp", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  thumb_format webp\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.ThumbFormat != "webp" {
			t.Errorf("ThumbFormat: got %q, want webp", g.ThumbFormat)
		}
	})
	t.Run("thumb_format avif → error (not in v1)", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  thumb_format avif\n}")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for thumb_format avif (not in v1)")
		}
	})
	t.Run("cache_scan 5", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  cache_scan 5\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.CacheScanMinutes != 5 {
			t.Errorf("CacheScanMinutes: got %d, want 5", g.CacheScanMinutes)
		}
	})
	t.Run("cache_scan 0 → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  cache_scan 0\n}")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for cache_scan 0")
		}
	})
	t.Run("thumb_ttl 60", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  thumb_ttl 60\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.ThumbTTLMinutes != 60 {
			t.Errorf("ThumbTTLMinutes: got %d, want 60", g.ThumbTTLMinutes)
		}
	})
	t.Run("thumb_ttl 0 → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  thumb_ttl 0\n}")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for thumb_ttl 0")
		}
	})
	t.Run("thumb_ttl with abc → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  thumb_ttl abc\n}")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for thumb_ttl abc")
		}
	})
	t.Run("all 5 directives together", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("image_gallery {\n  thumb_width 480\n  thumb_height 320\n  thumb_format jpeg\n  cache_scan 5\n  thumb_ttl 60\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.ThumbWidth != 480 || g.ThumbHeight != 320 || g.ThumbFormat != "jpeg" ||
			g.CacheScanMinutes != 5 || g.ThumbTTLMinutes != 60 {
			t.Errorf("got %+v, want all 5 set", g)
		}
	})
}

// TestProvision_ThumbConfigDefaults verifies that Provision applies
// the default values for the 5 new fields when the Caddyfile
// doesn't set them.
func TestProvision_ThumbConfigDefaults(t *testing.T) {
	g := Gallery{}
	if err := g.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	if g.ThumbWidth != 320 {
		t.Errorf("ThumbWidth default: got %d, want 320", g.ThumbWidth)
	}
	if g.ThumbHeight != 320 {
		t.Errorf("ThumbHeight default: got %d, want 320", g.ThumbHeight)
	}
	if g.ThumbFormat != "webp" {
		t.Errorf("ThumbFormat default: got %q, want webp", g.ThumbFormat)
	}
	if g.CacheScanMinutes != 1 {
		t.Errorf("CacheScanMinutes default: got %d, want 1", g.CacheScanMinutes)
	}
	if g.ThumbTTLMinutes != 1440 {
		t.Errorf("ThumbTTLMinutes default: got %d, want 1440 (= 24h)", g.ThumbTTLMinutes)
	}
}

// TestRenderPage_PageSizePagination verifies that the pageSize
// parameter is honored: with 7 images and pageSize=3, RenderPage
// produces a "Page 1 of 3" pagination header
// (ceil(7/3) = 3 pages). The first call exercises an explicit
// pageSize. (The default-pageSize case is covered by
// TestProvision_PageSizeDefault + the no-pagination-when-fits
// behavior below.)
func TestRenderPage_PageSizePagination(t *testing.T) {
	files := []FileInfo{
		{Name: "a.jpg", ModTime: 7, Size: 100, Kind: KindImage},
		{Name: "b.jpg", ModTime: 6, Size: 100, Kind: KindImage},
		{Name: "c.jpg", ModTime: 5, Size: 100, Kind: KindImage},
		{Name: "d.jpg", ModTime: 4, Size: 100, Kind: KindImage},
		{Name: "e.jpg", ModTime: 3, Size: 100, Kind: KindImage},
		{Name: "f.jpg", ModTime: 2, Size: 100, Kind: KindImage},
		{Name: "g.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	// pageSize=3 → 7 images / 3 per page = 3 pages, "Page 1 of 3"
	html, err := RenderPage(
		"test", "./", "./_thumbs/", "", "", false, false, 3, files, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "Page 1 of 3") {
		t.Errorf("expected 'Page 1 of 3' for 7 images @ page_size=3, got:\n%s",
			extractMetaSnippets(html))
	}
	// pageSize=0 → defaults to 50 → all 7 images fit on page 1 →
	// pagination block doesn't render (the template only renders
	// the nav when TotalPages > 1, see render.go around the
	// `if gt .TotalPages 1` block). This is the right behavior:
	// no point showing "Page 1 of 1" + disabled prev/next buttons.
	html, err = RenderPage(
		"test", "./", "./_thumbs/", "", "", false, false, 0, files, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, `<nav class="pagination">`) {
		t.Errorf("expected NO pagination nav when all 7 images fit on one page (page_size=0 → 50), got:\n%s",
			extractMetaSnippets(html))
	}
}

// extractMetaSnippets pulls short "N of M" / "Page N of M" style
// meta strings from the HTML. Used in failure messages for the
// page_size tests.
func extractMetaSnippets(html string) string {
	var out []string
	for _, line := range strings.Split(html, "\n") {
		if strings.Contains(line, "of") && (strings.Contains(line, "Page") ||
			strings.Contains(line, "image") || strings.Contains(line, "Image")) {
			out = append(out, "  "+strings.TrimSpace(line))
		}
	}
	return strings.Join(out, "\n")
}

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

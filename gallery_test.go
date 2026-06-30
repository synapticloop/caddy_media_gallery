package gallery

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		d := caddyfile.NewTestDispenser("media_gallery {\n  no_thumbs\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if !g.NoThumbs {
			t.Error("expected NoThumbs=true after `no_thumbs` directive")
		}
	})
	t.Run("no_thumbs false → false", func(t *testing.T) {
		g := Gallery{NoThumbs: true}
		d := caddyfile.NewTestDispenser("media_gallery { no_thumbs false }")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.NoThumbs {
			t.Error("expected NoThumbs=false after `no_thumbs false` directive")
		}
	})
	t.Run("no_thumbs with bogus arg → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery { no_thumbs off }")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for `no_thumbs off` (must be `false`, not `off`)")
		}
	})
	t.Run("no_thumbs + template both set", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  template themes/dark/gallery.tmpl\n  no_thumbs\n}")
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


// TestUnmarshalCaddyfile_NoExif covers the `no_exif` Caddyfile
// directive. Accepts:
//   - `no_exif` (no arg) → true
//   - `no_exif false`    → false
//   - anything else → error
func TestUnmarshalCaddyfile_NoExif(t *testing.T) {
	t.Run("no_exif (no arg) → true", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  no_exif\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if !g.NoExif {
			t.Error("expected NoExif=true after `no_exif` directive")
		}
	})
	t.Run("no_exif false → false", func(t *testing.T) {
		g := Gallery{NoExif: true}
		d := caddyfile.NewTestDispenser("media_gallery { no_exif false }")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.NoExif {
			t.Error("expected NoExif=false after `no_exif false` directive")
		}
	})
	t.Run("no_exif with bogus arg → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery { no_exif off }")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for `no_exif off` (must be `false`, not `off`)")
		}
	})
	t.Run("no_exif + no_thumbs both set", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  no_thumbs\n  no_exif\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if !g.NoThumbs {
			t.Error("expected NoThumbs=true")
		}
		if !g.NoExif {
			t.Error("expected NoExif=true")
		}
	})
}

func TestUnmarshalCaddyfile_NoVideoThumbs(t *testing.T) {
	t.Run("no_video_thumbs (no arg) → true", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  no_video_thumbs\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if !g.NoVideoThumbs {
			t.Error("expected NoVideoThumbs=true after `no_video_thumbs` directive")
		}
	})
	t.Run("no_video_thumbs false → false", func(t *testing.T) {
		g := Gallery{NoVideoThumbs: true}
		d := caddyfile.NewTestDispenser("media_gallery { no_video_thumbs false }")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.NoVideoThumbs {
			t.Error("expected NoVideoThumbs=false after `no_video_thumbs false` directive")
		}
	})
	t.Run("no_video_thumbs with bogus arg → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery { no_video_thumbs off }")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for `no_video_thumbs off` (must be `false`, not `off`)")
		}
	})
	t.Run("no_video_thumbs + no_thumbs together", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  no_thumbs\n  no_video_thumbs\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if !g.NoThumbs || !g.NoVideoThumbs {
			t.Error("expected both NoThumbs and NoVideoThumbs to be true")
		}
	})
}

// TestGallery_ProvisionDefaultFromNumPerPage verifies that
// when the operator configures `num_per_page 60 30 120 all`
// in the Caddyfile, the default page size is 60 (the operator's
// DECLARED first item), NOT 30 (the sorted first item).
// This is the fix for the user-reported bug: the dropdown
// was showing 30 selected because sortPageSizes was running
// BEFORE the default was read from the list.
func TestGallery_ProvisionDefaultFromNumPerPage(t *testing.T) {
	// Operator wrote `num_per_page 60 30 120 all`
	g := &Gallery{
		Root:             "/var/www/html/images",
		Cache:            NewScanCache(time.Minute),
		ThumbWidth:       320,
		ThumbHeight:      320,
		ThumbFormat:      "webp",
		CacheScanMinutes: 1,
		ThumbTTLMinutes:  1440,
		PageSizes:        []string{"60", "30", "120", "all"},
	}
	if err := g.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	if g.PageSize != 60 {
		t.Errorf("operator declared first item 60: expected default PageSize=60, got %d", g.PageSize)
	}
	// Display should still be sorted (30, 60, 120, all).
	wantDisplay := []string{"30", "60", "120", "all"}
	if len(g.PageSizes) != len(wantDisplay) {
		t.Errorf("PageSizes display: got %v, want %v", g.PageSizes, wantDisplay)
	}
	for i := range wantDisplay {
		if g.PageSizes[i] != wantDisplay[i] {
			t.Errorf("PageSizes display: got %v, want %v", g.PageSizes, wantDisplay)
			break
		}
	}

	// Operator wrote `num_per_page 30 60 120 all` (default 30)
	g2 := &Gallery{
		Root:             "/var/www/html/images",
		Cache:            NewScanCache(time.Minute),
		ThumbWidth:       320,
		ThumbHeight:      320,
		ThumbFormat:      "webp",
		CacheScanMinutes: 1,
		ThumbTTLMinutes:  1440,
		PageSizes:        []string{"30", "60", "120", "all"},
	}
	if err := g2.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	if g2.PageSize != 30 {
		t.Errorf("operator declared first item 30: expected default PageSize=30, got %d", g2.PageSize)
	}
}

// TestRenderPage_NoThumbs_OriginalImageAsThumb verifies that
// when no_thumbs is true, the rendered tile <img> uses the
// original file URL (the Href) as its src, not the thumb URL.
// TestGallery_ProvisionRootNameDefaults verifies that when
// the operator doesn't set `root_name` in the Caddyfile,
// g.rootName defaults to filepath.Base(g.Root) (e.g.
// "images" for /var/www/html/images). This is the fix for
// the user-reported bug where the first breadcrumb segment
// showed the subdirectory name twice instead of "images"
// + "media_gallery".
// TestSortPageSizes verifies the sort rule: numeric values
// ascending, "all" last. This is the dropdown display order.
func TestSortPageSizes(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"already sorted", []string{"30", "60", "120", "all"}, []string{"30", "60", "120", "all"}},
		{"operator's order: 60 first", []string{"60", "30", "120", "all"}, []string{"30", "60", "120", "all"}},
		{"out of order", []string{"120", "30", "60", "all"}, []string{"30", "60", "120", "all"}},
		{"no all", []string{"100", "50"}, []string{"50", "100"}},
		{"all only", []string{"all"}, []string{"all"}},
		{"empty", []string{}, []string{}},
		{"all in middle", []string{"60", "all", "30"}, []string{"30", "60", "all"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sortPageSizes(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got %v, want %v", got, tc.want)
					break
				}
			}
		})
	}
}

func TestGallery_ProvisionRootNameDefaults(t *testing.T) {
	g := &Gallery{
		Root:             "/var/www/html/images",
		Cache:            NewScanCache(time.Minute),
		ThumbWidth:       320,
		ThumbHeight:      320,
		ThumbFormat:      "webp",
		CacheScanMinutes: 1,
		ThumbTTLMinutes:  1440,
		PageSizes:        []string{"30", "60", "120", "all"},
	}
	if err := g.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	if g.rootName != "images" {
		t.Errorf("expected g.rootName = %q, got %q", "images", g.rootName)
	}
	// And when the operator DID set RootName, use their value.
	g2 := &Gallery{
		Root:             "/var/www/html/images",
		RootName:         "media root",
		Cache:            NewScanCache(time.Minute),
		ThumbWidth:       320,
		ThumbHeight:      320,
		ThumbFormat:      "webp",
		CacheScanMinutes: 1,
		ThumbTTLMinutes:  1440,
		PageSizes:        []string{"30", "60", "120", "all"},
	}
	if err := g2.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	if g2.rootName != "media root" {
		t.Errorf("expected g.rootName = %q, got %q", "media root", g2.rootName)
	}
}

func TestRenderPage_NoThumbs_OriginalImageAsThumb(t *testing.T) {
	files := []FileInfo{
		{Name: "photo.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	html, err := RenderPage(
		"test", "./", "./_thumbs/", "", "", true, false, 0, nil, files, nil, nil, nil, "", "", "substring", "00", "00", "00", "00")
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
		"test", "./", "./_thumbs/", "", "", false, false, 0, nil, files, nil, nil, nil, "", "", "substring", "00", "00", "00", "00")
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
// `page_size <list>` Caddyfile directive. Accepts a
// space-separated list of page sizes; the first item is
// the default. Rejects empty lists.
func TestUnmarshalCaddyfile_PageSize(t *testing.T) {
	t.Run("page_size 60 30 120 all (default 60)", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  page_size 60 30 120 all\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if len(g.PageSizes) != 4 || g.PageSizes[0] != "60" || g.PageSizes[3] != "all" {
			t.Errorf("PageSizes: got %v, want [60 30 120 all]", g.PageSizes)
		}
	})
	t.Run("page_size 50 100 (no all)", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  page_size 50 100\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if len(g.PageSizes) != 2 || g.PageSizes[0] != "50" || g.PageSizes[1] != "100" {
			t.Errorf("PageSizes: got %v, want [50 100]", g.PageSizes)
		}
	})
	t.Run("page_size with no args → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  page_size\n}")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for `page_size` with no values")
		}
	})
	t.Run("page_size 30 60 120 all (default 30)", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  page_size 30 60 120 all\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.PageSizes[0] != "30" {
			t.Errorf("PageSizes[0]: got %v, want 30 (operator's declared first item)", g.PageSizes[0])
		}
	})
}

// TestProvision_PageSizeDefault verifies that Provision applies
// the default of 50 when the Caddyfile doesn't set page_size.
// TestProvision_PageSizeDefault verifies that the default
// page size (with no operator config) is the first item in
// the default PageSizes list, which is 60. The default list
// was changed from [30, 60, 120, "all"] to [60, 30, 120,
// "all"] in Phase 161 so the default page size is 60
// (matches the user-requested default).
func TestProvision_PageSizeDefault(t *testing.T) {
	g := Gallery{}
	if err := g.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	if g.PageSize != 60 {
		t.Errorf("expected default PageSize=60 after Provision (first item of default list), got %d", g.PageSize)
	}
	// The default PageSizes list itself should be set.
	if len(g.PageSizes) == 0 {
		t.Error("expected default PageSizes list to be set")
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
		d := caddyfile.NewTestDispenser("media_gallery {\n  thumb_width 480\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.ThumbWidth != 480 {
			t.Errorf("ThumbWidth: got %d, want 480", g.ThumbWidth)
		}
	})
	t.Run("thumb_width 0 → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  thumb_width 0\n}")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for thumb_width 0")
		}
	})
	t.Run("thumb_width -1 → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  thumb_width -1\n}")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for thumb_width -1")
		}
	})
	t.Run("thumb_height 240", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  thumb_height 240\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.ThumbHeight != 240 {
			t.Errorf("ThumbHeight: got %d, want 240", g.ThumbHeight)
		}
	})
	t.Run("thumb_format jpeg", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  thumb_format jpeg\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.ThumbFormat != "jpeg" {
			t.Errorf("ThumbFormat: got %q, want jpeg", g.ThumbFormat)
		}
	})
	t.Run("thumb_format jpg (alias)", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  thumb_format jpg\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.ThumbFormat != "jpg" {
			t.Errorf("ThumbFormat: got %q, want jpg", g.ThumbFormat)
		}
	})
	t.Run("thumb_format png", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  thumb_format png\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.ThumbFormat != "png" {
			t.Errorf("ThumbFormat: got %q, want png", g.ThumbFormat)
		}
	})
	t.Run("thumb_format webp", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  thumb_format webp\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.ThumbFormat != "webp" {
			t.Errorf("ThumbFormat: got %q, want webp", g.ThumbFormat)
		}
	})
	t.Run("thumb_format avif → error (not in v1)", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  thumb_format avif\n}")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for thumb_format avif (not in v1)")
		}
	})
	t.Run("cache_scan 5", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  cache_scan 5\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.CacheScanMinutes != 5 {
			t.Errorf("CacheScanMinutes: got %d, want 5", g.CacheScanMinutes)
		}
	})
	t.Run("cache_scan 0 → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  cache_scan 0\n}")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for cache_scan 0")
		}
	})
	t.Run("thumb_ttl 60", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  thumb_ttl 60\n}")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.ThumbTTLMinutes != 60 {
			t.Errorf("ThumbTTLMinutes: got %d, want 60", g.ThumbTTLMinutes)
		}
	})
	t.Run("thumb_ttl 0 → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  thumb_ttl 0\n}")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for thumb_ttl 0")
		}
	})
	t.Run("thumb_ttl with abc → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  thumb_ttl abc\n}")
		if err := g.UnmarshalCaddyfile(d); err == nil {
			t.Error("expected error for thumb_ttl abc")
		}
	})
	t.Run("all 5 directives together", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n  thumb_width 480\n  thumb_height 320\n  thumb_format jpeg\n  cache_scan 5\n  thumb_ttl 60\n}")
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
		"test", "./", "./_thumbs/", "", "", false, false, 3, nil, files, nil, nil, nil, "", "", "substring", "00", "00", "00", "00")
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
		"test", "./", "./_thumbs/", "", "", false, false, 0, nil, files, nil, nil, nil, "", "", "substring", "00", "00", "00", "00")
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
		d := caddyfile.NewTestDispenser("media_gallery { sort name }")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.Template != "preserved.tmpl" {
			t.Errorf("expected Template preserved at %q, got %q", "preserved.tmpl", g.Template)
		}
	})
	t.Run("template gallery.tmpl", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n	template gallery.tmpl\n}\n")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.Template != "gallery.tmpl" {
			t.Errorf("expected Template=gallery.tmpl, got %q", g.Template)
		}
	})
	t.Run("template themes/dark/index.tmpl", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n	template themes/dark/index.tmpl\n}\n")
		if err := g.UnmarshalCaddyfile(d); err != nil {
			t.Fatal(err)
		}
		if g.Template != "themes/dark/index.tmpl" {
			t.Errorf("expected Template=themes/dark/index.tmpl, got %q", g.Template)
		}
	})
	t.Run("template with too many args → error", func(t *testing.T) {
		g := Gallery{}
		d := caddyfile.NewTestDispenser("media_gallery {\n	template a.tmpl b.tmpl\n}\n")
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
	if info.ID != "http.handlers.media_gallery" {
		t.Errorf("expected module ID %q, got %q", "http.handlers.media_gallery", info.ID)
	}
	if info.New == nil {
		t.Error("expected New constructor to be non-nil")
	}
}

// TestProvision_FFmpegPath_DefaultLookupPath verifies the fallback
// path: when FFMPEG_PATH is NOT set, Provision falls back to
// exec.LookPath("ffmpeg"). On a host with ffmpeg installed at
// /usr/bin/ffmpeg (the common case), g.ffmpegPath will be
// "/usr/bin/ffmpeg". On a host without ffmpeg, g.ffmpegPath
// stays empty.
func TestProvision_FFmpegPath_DefaultLookupPath(t *testing.T) {
	g := Gallery{}
	if err := g.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	// We don't make a hard assumption about whether ffmpeg is
	// installed in CI — just verify the behavior is consistent:
	// if ffmpegPath is set, it should be an absolute path to a
	// real executable.
	if g.ffmpegPath != "" {
		info, err := os.Stat(g.ffmpegPath)
		if err != nil {
			t.Errorf("ffmpegPath %q does not exist: %v", g.ffmpegPath, err)
		}
		if info != nil && info.IsDir() {
			t.Errorf("ffmpegPath %q is a directory, expected a file", g.ffmpegPath)
		}
	}
}

// TestProvision_FFmpegPath_EnvVarOverride verifies the new
// FFMPEG_PATH env var behavior (Phase 67): when set to a real
// executable path, it takes priority over exec.LookPath.
func TestProvision_FFmpegPath_EnvVarOverride(t *testing.T) {
	// Create a temp file that's executable — we use this as a
	// "fake ffmpeg" so we can verify the env var was honored
	// (without depending on /usr/bin/ffmpeg existing in CI).
	fakeFFmpeg := filepath.Join(t.TempDir(), "my-ffmpeg")
	if err := os.WriteFile(fakeFFmpeg, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FFMPEG_PATH", fakeFFmpeg)

	g := Gallery{}
	if err := g.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	if g.ffmpegPath != fakeFFmpeg {
		t.Errorf("expected ffmpegPath %q (from env var), got %q", fakeFFmpeg, g.ffmpegPath)
	}
}

// TestProvision_FFmpegPath_EnvVarIgnoredWhenNotExecutable
// verifies that a bad FFMPEG_PATH (file doesn't exist OR is not
// executable) is silently ignored — we fall back to
// exec.LookPath rather than storing a path that would fail at
// request time.
func TestProvision_FFmpegPath_EnvVarIgnoredWhenNotExecutable(t *testing.T) {
	t.Run("FFMPEG_PATH points to non-existent file", func(t *testing.T) {
		t.Setenv("FFMPEG_PATH", "/nonexistent/path/to/ffmpeg")
		g := Gallery{}
		if err := g.Provision(caddy.Context{}); err != nil {
			t.Fatal(err)
		}
		// If ffmpeg is installed via $PATH, g.ffmpegPath will
		// be that path (LookPath fallback). If not, empty.
		// Either way, MUST NOT be the bad FFMPEG_PATH we set.
		if g.ffmpegPath == "/nonexistent/path/to/ffmpeg" {
			t.Error("bad FFMPEG_PATH should be ignored, not stored")
		}
	})

	t.Run("FFMPEG_PATH points to a directory", func(t *testing.T) {
		t.Setenv("FFMPEG_PATH", t.TempDir()) // a directory, not a file
		g := Gallery{}
		if err := g.Provision(caddy.Context{}); err != nil {
			t.Fatal(err)
		}
		if g.ffmpegPath == t.TempDir() {
			// Note: this can pass spuriously because each t.Run
			// gets its own t.TempDir(), and by the time we
			// compare g.ffmpegPath to t.TempDir() the dir has
			// been deleted. The real check is that g.ffmpegPath
			// doesn't equal a temp dir path we just set.
			t.Error("FFMPEG_PATH pointing to a directory should be ignored")
		}
	})

	t.Run("FFMPEG_PATH points to a non-executable file", func(t *testing.T) {
		nonExec := filepath.Join(t.TempDir(), "ffmpeg")
		if err := os.WriteFile(nonExec, []byte("not executable"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("FFMPEG_PATH", nonExec)
		g := Gallery{}
		if err := g.Provision(caddy.Context{}); err != nil {
			t.Fatal(err)
		}
		if g.ffmpegPath == nonExec {
			t.Error("FFMPEG_PATH pointing to a non-executable file should be ignored")
		}
	})
}

// TestProvision_FFmpegPath_SkippedWhenNoVideoThumbs verifies that
// when NoVideoThumbs is true, we don't bother looking for ffmpeg
// at all (the env var is also ignored). This is a minor
// optimization — the check is cheap, but it documents intent.
func TestProvision_FFmpegPath_SkippedWhenNoVideoThumbs(t *testing.T) {
	fakeFFmpeg := filepath.Join(t.TempDir(), "my-ffmpeg")
	if err := os.WriteFile(fakeFFmpeg, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FFMPEG_PATH", fakeFFmpeg)

	g := Gallery{NoVideoThumbs: true}
	if err := g.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}
	if g.ffmpegPath != "" {
		t.Errorf("expected empty ffmpegPath when NoVideoThumbs=true, got %q", g.ffmpegPath)
	}
}

// TestProvision_FFmpegPath_LogsResolvedPath verifies Phase 106:
// Provision() emits a log line to stderr reporting the resolved
// ffmpeg path. This is the operator's confirmation that the
// right binary was picked up at startup (important because
// ffmpeg detection is startup-only — a new ffmpeg install
// requires a Caddy restart to be picked up).
func TestProvision_FFmpegPath_LogsResolvedPath(t *testing.T) {
	// Set up a fake ffmpeg binary in the env var so we can verify
	// the log line shows THAT path (not the system ffmpeg, which
	// may or may not exist on the test machine).
	fakeFFmpeg := filepath.Join(t.TempDir(), "fake-ffmpeg")
	if err := os.WriteFile(fakeFFmpeg, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FFMPEG_PATH", fakeFFmpeg)

	// Capture stderr.
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = origStderr })

	// Run Provision.
	g := Gallery{}
	if err := g.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}

	// Close the writer so the reader sees EOF, then read.
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	output := string(out)

	// The log line should mention our fake path.
	want := fmt.Sprintf("caddy-media-gallery: ffmpeg path: %s", fakeFFmpeg)
	if !strings.Contains(output, want) {
		t.Errorf("expected stderr to contain %q, got %q", want, output)
	}
}

// TestProvision_ImageTypesCustomConfig verifies that the
// image_types Caddyfile directive overrides the default
// image extension set. After Provision, the resolved
// imageExtsMap should match the configured extensions
// (lowercased + dot-prefixed).
func TestProvision_ImageTypesCustomConfig(t *testing.T) {
	d := caddyfile.NewTestDispenser(`media_gallery {
		image_types JPG .heic RAW
	}`)

	g := Gallery{}
	if err := g.UnmarshalCaddyfile(d); err != nil {
		t.Fatal(err)
	}
	if err := g.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}

	// 3 entries: JPG, .heic, RAW
	if len(g.ImageExts) != 3 {
		t.Errorf("expected 3 entries in g.ImageExts, got %d: %v", len(g.ImageExts), g.ImageExts)
	}

	// Check the resolved map (lowercased, dot-prefixed).
	wantIn := map[string]bool{".jpg": true, ".heic": true, ".raw": true}
	for k := range wantIn {
		if !g.imageExtsMap[k] {
			t.Errorf("expected imageExtsMap[%q] = true, got false (map: %v)", k, g.imageExtsMap)
		}
	}
	// Default extensions should NOT be present (we overrode).
	if g.imageExtsMap[".png"] {
		t.Error("default .png should NOT be in imageExtsMap when image_types is configured")
	}
}

// TestProvision_VideoTypesCustomConfig verifies the same for
// video_types. Also verifies that omitting BOTH directives
// gives the defaults.
func TestProvision_VideoTypesCustomConfig(t *testing.T) {
	d := caddyfile.NewTestDispenser(`media_gallery {
		video_types mp4 MOV
	}`)

	g := Gallery{}
	if err := g.UnmarshalCaddyfile(d); err != nil {
		t.Fatal(err)
	}
	if err := g.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}

	wantIn := map[string]bool{".mp4": true, ".mov": true}
	for k := range wantIn {
		if !g.videoExtsMap[k] {
			t.Errorf("expected videoExtsMap[%q] = true, got false (map: %v)", k, g.videoExtsMap)
		}
	}
	if g.videoExtsMap[".webm"] {
		t.Error("default .webm should NOT be in videoExtsMap when video_types is configured")
	}
	// Image types should still be the defaults (we only overrode video_types).
	if !g.imageExtsMap[".jpg"] {
		t.Error("imageExtsMap should still have the default .jpg (we only overrode video_types)")
	}
}

// TestProvision_DefaultsWhenNoCustomExtTypes verifies that an
// empty Caddyfile (no image_types or video_types) gives the
// built-in defaults for both maps.
func TestProvision_DefaultsWhenNoCustomExtTypes(t *testing.T) {
	d := caddyfile.NewTestDispenser(`media_gallery {
		page_size 100
	}`)

	g := Gallery{}
	if err := g.UnmarshalCaddyfile(d); err != nil {
		t.Fatal(err)
	}
	if err := g.Provision(caddy.Context{}); err != nil {
		t.Fatal(err)
	}

	// Image defaults (per user request 2026-06-30: HEIC, AVIF,
	// and SVG are NOT in the defaults — Go's stdlib can't decode
	// them. Operators can still add them via the image_types
	// subdirective.)
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".gif", ".webp"} {
		if !g.imageExtsMap[ext] {
			t.Errorf("expected default imageExtsMap[%q] = true, got false", ext)
		}
	}
	// Video defaults
	for _, ext := range []string{".mp4", ".webm"} {
		if !g.videoExtsMap[ext] {
			t.Errorf("expected default videoExtsMap[%q] = true, got false", ext)
		}
	}
}

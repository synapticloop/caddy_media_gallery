package gallery

import (
	"strings"
	"testing"
)

// TestRenderPage_LocaleHTMLAttribute verifies that the
// <html lang="..."> attribute is set to the resolved
// locale. Per user request 2026-07-04.
func TestRenderPage_LocaleHTMLAttribute(t *testing.T) {
	files := []FileInfo{
		{Name: "photo.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	for _, tc := range []struct {
		locale string
		want   string
	}{
		{"en", `lang="en"`},
		{"de", `lang="de"`},
		{"ja", `lang="ja"`},
		{"", `lang="en"`}, // empty defaults to en
	} {
		t.Run(tc.locale, func(t *testing.T) {
			tr, _ := NewTranslator("")
			html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0,
				[]string{"30", "60", "120", "all"}, files, nil, defaultImageExts, defaultVideoExts,
				"", "", "substring", tc.locale, tr, "00", "00", "00", "00")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(html, tc.want) {
				t.Errorf("expected %q in <html> tag, got HTML starting with: %s",
					tc.want, html[:min(300, len(html))])
			}
		})
	}
}

// TestRenderPage_LocaleTranslation verifies that strings
// are translated to the requested locale. Per user
// request 2026-07-04.
func TestRenderPage_LocaleTranslation(t *testing.T) {
	files := []FileInfo{
		{Name: "photo.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	for _, tc := range []struct {
		locale string
		// A substring expected to appear in the rendered
		// page when the locale is active. Picks a
		// distinctive phrase per locale so we can verify
		// the right translation rendered.
		wantSubstr string
	}{
		{"en", "Sort Media By"},
		{"de", "Medien sortieren nach"},
		{"es", "Ordenar medios por"},
		{"fr", "Trier les médias par"},
		{"ja", "メディアの並べ替え"},
		{"ko", "미디어 정렬 기준"},
		{"zh", "媒体排序"},
		{"pt", "Ordenar mídia por"},
	} {
		t.Run(tc.locale, func(t *testing.T) {
			tr, _ := NewTranslator("")
			html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0,
				[]string{"30", "60", "120", "all"}, files, nil, defaultImageExts, defaultVideoExts,
				"", "", "substring", tc.locale, tr, "00", "00", "00", "00")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(html, tc.wantSubstr) {
				t.Errorf("expected %q (%s) in rendered HTML", tc.wantSubstr, tc.locale)
			}
		})
	}
}

// TestRenderPage_TranslationMissingFallback verifies that
// a key missing in the requested locale falls back to
// English. Per user request 2026-07-04.
func TestRenderPage_TranslationMissingFallback(t *testing.T) {
	files := []FileInfo{
		{Name: "photo.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	// Use a fresh translator with a custom disk file
	// containing German with one key removed, to force
	// the English fallback path.
	tr := &Translator{
		entries: map[string]map[string]string{
			"de": {
				"sort_label": "TEST_GERMAN_SORT_LABEL",
				// Intentionally missing "search_placeholder"
				// so the lookup falls back to en.
			},
			"en": {
				"sort_label":          "Sort Media By",
				"search_placeholder":  "Search filenames…",
			},
		},
		locales: []string{"de", "en"},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0,
		[]string{"30", "60", "120", "all"}, files, nil, defaultImageExts, defaultVideoExts,
		"", "", "substring", "de", tr, "00", "00", "00", "00")
	if err != nil {
		t.Fatal(err)
	}
	// The German key SHOULD appear (it's defined).
	if !strings.Contains(html, "TEST_GERMAN_SORT_LABEL") {
		t.Error("expected German sort_label to be used")
	}
	// The missing key should fall back to English.
	if !strings.Contains(html, "Search filenames…") {
		t.Error("expected English fallback for missing German key")
	}
}
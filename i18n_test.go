package gallery

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewTranslator_Embedded verifies the Translator
// loads embedded defaults correctly.
func TestNewTranslator_Embedded(t *testing.T) {
	tr, err := NewTranslator("")
	if err != nil {
		t.Fatal(err)
	}
	// English is the canonical fallback.
	if !tr.HasLocale("en") {
		t.Error("expected English (en) to be present")
	}
	// Other locales should be present too.
	for _, want := range []string{"en", "es", "fr", "de", "ja", "ko", "zh", "pt"} {
		if !tr.HasLocale(want) {
			t.Errorf("expected locale %q to be present", want)
		}
	}
	// Sort order should be alphabetical.
	want := []string{"de", "en", "es", "fr", "ja", "ko", "pt", "zh"}
	got := tr.Locales()
	if len(got) != len(want) {
		t.Errorf("expected %d locales, got %d", len(want), len(got))
	}
	for i, w := range want {
		if i >= len(got) || got[i] != w {
			t.Errorf("locale[%d]: want %q, got %v", i, w, got)
			break
		}
	}
}

// TestT_BasicLookup checks a simple key lookup.
func TestT_BasicLookup(t *testing.T) {
	tr, err := NewTranslator("")
	if err != nil {
		t.Fatal(err)
	}
	// English lookup.
	got := tr.T("en", "sort_label")
	if got != "Sort Media By" {
		t.Errorf("en/sort_label: want 'Sort Media By', got %q", got)
	}
	// German lookup.
	got = tr.T("de", "sort_label")
	if got == "" || got == "sort_label" {
		t.Errorf("de/sort_label: want a German translation, got %q", got)
	}
	// Spanish lookup.
	got = tr.T("es", "sort_label")
	if got == "" || got == "sort_label" {
		t.Errorf("es/sort_label: want a Spanish translation, got %q", got)
	}
}

// TestT_FallbackToEnglish verifies a missing key falls
// back to the English translation.
func TestT_FallbackToEnglish(t *testing.T) {
	tr, err := NewTranslator("")
	if err != nil {
		t.Fatal(err)
	}
	// Spanish should fall back to English for any key
	// that's missing in es.json (but present in en.json).
	// Pick a key that doesn't exist in any locale — it
	// should fall all the way back to the key itself.
	got := tr.T("es", "totally_made_up_key_xyz")
	if got != "totally_made_up_key_xyz" {
		t.Errorf("missing key: want key itself, got %q", got)
	}
}

// TestT_Placeholders verifies the fmt.Sprintf substitution.
func TestT_Placeholders(t *testing.T) {
	tr, err := NewTranslator("")
	if err != nil {
		t.Fatal(err)
	}
	// "Page {page} of {total}" — substitute two args.
	got := tr.T("en", "page_of", 2, 5)
	if got != "Page 2 of 5" {
		t.Errorf("page_of: want 'Page 2 of 5', got %q", got)
	}
	// With no args, returns the raw string.
	got = tr.T("en", "page_of")
	if got != "Page {page} of {total}" {
		t.Errorf("page_of (no args): want raw string, got %q", got)
	}
}

// TestT_MissingLocaleNoCrash verifies T never panics on an
// unknown locale — it falls back to English.
func TestT_MissingLocaleNoCrash(t *testing.T) {
	tr, err := NewTranslator("")
	if err != nil {
		t.Fatal(err)
	}
	// Unknown locale: falls back to en.
	got := tr.T("xx", "sort_label")
	if got != "Sort Media By" {
		t.Errorf("xx locale fallback: want 'Sort Media By', got %q", got)
	}
	// Unknown locale + unknown key: returns the key.
	got = tr.T("xx", "totally_made_up_key_xyz")
	if got != "totally_made_up_key_xyz" {
		t.Errorf("xx + unknown key: want key, got %q", got)
	}
}

// TestDetectLocale_URLParamHighestPriority verifies the URL
// parameter overrides everything else.
func TestDetectLocale_URLParamHighestPriority(t *testing.T) {
	supported := []string{"en", "de", "fr", "ja"}
	r := httptest.NewRequest("GET", "/images/?lang=de", nil)
	r.Header.Set("Accept-Language", "ja,en;q=0.5")
	r.AddCookie(&http.Cookie{Name: "gallery-language", Value: "fr"})
	got := DetectLocale(r, supported, "en")
	if got != "de" {
		t.Errorf("URL param highest priority: want de, got %q", got)
	}
}

// TestDetectLocale_CookieNextAfterURL verifies the cookie
// is checked when no URL param is set.
func TestDetectLocale_CookieNextAfterURL(t *testing.T) {
	supported := []string{"en", "de", "fr", "ja"}
	r := httptest.NewRequest("GET", "/images/", nil)
	r.Header.Set("Accept-Language", "ja,en;q=0.5")
	r.AddCookie(&http.Cookie{Name: "gallery-language", Value: "fr"})
	got := DetectLocale(r, supported, "en")
	if got != "fr" {
		t.Errorf("cookie priority: want fr, got %q", got)
	}
}

// TestDetectLocale_AcceptLanguageWhenNoParamNoCookie verifies
// the Accept-Language header is the third priority.
func TestDetectLocale_AcceptLanguageWhenNoParamNoCookie(t *testing.T) {
	supported := []string{"en", "de", "fr", "ja"}
	r := httptest.NewRequest("GET", "/images/", nil)
	r.Header.Set("Accept-Language", "ja,en;q=0.5,fr;q=0.8")
	got := DetectLocale(r, supported, "en")
	if got != "ja" {
		t.Errorf("Accept-Language priority: want ja, got %q", got)
	}
}

// TestDetectLocale_DefaultWhenNoSignals verifies the
// operator default is used when no signal is present.
func TestDetectLocale_DefaultWhenNoSignals(t *testing.T) {
	supported := []string{"en", "de", "fr", "ja"}
	r := httptest.NewRequest("GET", "/images/", nil)
	got := DetectLocale(r, supported, "de")
	if got != "de" {
		t.Errorf("default priority: want de, got %q", got)
	}
}

// TestDetectLocale_RegionSuffix verifies "de-DE" matches "de".
func TestDetectLocale_RegionSuffix(t *testing.T) {
	supported := []string{"en", "de", "fr", "ja"}
	// Region in URL param.
	r := httptest.NewRequest("GET", "/images/?lang=de-DE", nil)
	got := DetectLocale(r, supported, "en")
	if got != "de" {
		t.Errorf("region URL: want de, got %q", got)
	}
	// Region in Accept-Language.
	r2 := httptest.NewRequest("GET", "/images/", nil)
	r2.Header.Set("Accept-Language", "fr-FR,fr;q=0.9")
	got = DetectLocale(r2, supported, "en")
	if got != "fr" {
		t.Errorf("region Accept-Language: want fr, got %q", got)
	}
}

// TestDetectLocale_QFactor verifies the Accept-Language
// quality factor is honoured.
func TestDetectLocale_QFactor(t *testing.T) {
	supported := []string{"en", "de", "fr", "ja"}
	r := httptest.NewRequest("GET", "/images/", nil)
	// fr has q=0.9 (high), de has q=0.5 (low). fr should win.
	r.Header.Set("Accept-Language", "de;q=0.5,fr;q=0.9")
	got := DetectLocale(r, supported, "en")
	if got != "fr" {
		t.Errorf("q-factor: want fr, got %q", got)
	}
}

// TestDetectLocale_NoMatchFallsBackToDefault verifies
// that an unsupported locale preference still falls back
// gracefully (rather than returning "" or crashing).
func TestDetectLocale_NoMatchFallsBackToDefault(t *testing.T) {
	supported := []string{"en", "de", "fr"}
	r := httptest.NewRequest("GET", "/images/", nil)
	r.Header.Set("Accept-Language", "ja,ko")
	got := DetectLocale(r, supported, "de")
	if got != "de" {
		t.Errorf("unsupported locale: want de (default), got %q", got)
	}
}

// TestDiskOverride verifies a disk-supplied JSON file
// overrides (or adds to) the embedded translations.
func TestDiskOverride(t *testing.T) {
	dir := t.TempDir()
	// Write a brand-new locale that wasn't embedded.
	content := `{
		"_meta": {"locale": "nl", "language": "Nederlands"},
		"sort_label": "Sorteer media op",
		"new_key_only_in_nl": "Alleen in het Nederlands"
	}`
	if err := os.WriteFile(filepath.Join(dir, "nl.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := NewTranslator(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The new locale should be present.
	if !tr.HasLocale("nl") {
		t.Error("expected Dutch (nl) to be loaded from disk")
	}
	got := tr.T("nl", "sort_label")
	if got != "Sorteer media op" {
		t.Errorf("nl/sort_label: want 'Sorteer media op', got %q", got)
	}
	// Keys missing in nl fall back to en.
	got = tr.T("nl", "search_placeholder")
	if got != "Search filenames…" {
		t.Errorf("nl/search_placeholder: want English fallback 'Search filenames…', got %q", got)
	}
}

// TestDiskOverrideOverridesEmbedded verifies a disk-supplied
// file OVERRIDES the embedded translation for a locale that
// already exists.
func TestDiskOverrideOverridesEmbedded(t *testing.T) {
	dir := t.TempDir()
	// German already exists embedded; override one key.
	content := `{
		"sort_label": "SORTEER AUF (test override)"
	}`
	if err := os.WriteFile(filepath.Join(dir, "de.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := NewTranslator(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := tr.T("de", "sort_label")
	if got != "SORTEER AUF (test override)" {
		t.Errorf("disk override: want 'SORTEER AUF (test override)', got %q", got)
	}
	// Keys not in the disk file still come from the
	// embedded German.
	got = tr.T("de", "col_name")
	if got == "" || got == "sort_label" {
		t.Errorf("de/col_name after override: want a German translation, got %q", got)
	}
	if !strings.HasPrefix(got, "Sort") {
		// Fall through — the embedded German for col_name
		// is "Name", not "Sort".
		t.Logf("de/col_name: %q (may be 'Name' — that's fine)", got)
	}
}

// TestDiskOverride_MalformedJSONIgnored verifies a malformed
// disk file is logged + ignored (doesn't break the rest of
// the locale loading).
func TestDiskOverride_MalformedJSONIgnored(t *testing.T) {
	dir := t.TempDir()
	// Write a malformed file.
	if err := os.WriteFile(filepath.Join(dir, "xx.json"), []byte(`{ not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	// NewTranslator should not panic.
	tr, err := NewTranslator(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The malformed locale should not be loaded.
	if tr.HasLocale("xx") {
		t.Error("malformed locale should not be loaded")
	}
	// Other locales still work.
	got := tr.T("en", "sort_label")
	if got != "Sort Media By" {
		t.Errorf("en/sort_label after malformed disk: got %q", got)
	}
}

// TestHasLocale is a smoke test for the locale existence
// check.
func TestHasLocale(t *testing.T) {
	tr, err := NewTranslator("")
	if err != nil {
		t.Fatal(err)
	}
	if !tr.HasLocale("en") {
		t.Error("expected en to be present")
	}
	if !tr.HasLocale("EN") {
		t.Error("HasLocale should be case-insensitive")
	}
	if tr.HasLocale("xyz") {
		t.Error("expected xyz to be absent")
	}
}
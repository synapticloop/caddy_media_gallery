package gallery

import (
	"testing"
)

// TestTranslator_NativeName verifies the NativeName helper
// returns the locale's native-script name from the locale's
// OWN translation file. Per user request 2026-07-04:
// the dropdown shows native names ("English", "Deutsch",
// "日本語") regardless of the visitor's current locale.
func TestTranslator_NativeName(t *testing.T) {
	tr, err := NewTranslator("")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		locale string
		want   string
	}{
		// Each locale's native name (in its own script).
		// The expectation matches what we put in each
		// locale's JSON file's "lang_name_<locale>" key.
		{"en", "English"},
		{"de", "Deutsch"},
		{"es", "Español"},
		{"fr", "Français"},
		{"ja", "日本語"},
		{"ko", "한국어"},
		{"zh", "中文"},
		{"pt", "Português"},
	}
	for _, tc := range cases {
		t.Run(tc.locale, func(t *testing.T) {
			got := tr.NativeName(tc.locale)
			if got != tc.want {
				t.Errorf("NativeName(%q): want %q, got %q", tc.locale, tc.want, got)
			}
		})
	}
}

// TestTranslator_NativeName_Fallback verifies the fallback
// when a locale has no entry in any translation file
// (shouldn't happen in production but defensive).
func TestTranslator_NativeName_Fallback(t *testing.T) {
	tr, err := NewTranslator("")
	if err != nil {
		t.Fatal(err)
	}
	// Unknown locale — no entries in any file.
	got := tr.NativeName("xyz")
	if got != "xyz" {
		t.Errorf("NativeName(\"xyz\"): want fallback to locale code, got %q", got)
	}
}

// TestTranslator_LangNameKeys_AllPresent verifies that
// every language file has a lang_name_* entry for every
// other locale. Missing entries would fall back to
// less-useful text in the language picker.
func TestTranslator_LangNameKeys_AllPresent(t *testing.T) {
	tr, err := NewTranslator("")
	if err != nil {
		t.Fatal(err)
	}
	expectedKeys := []string{
		"lang_name_en", "lang_name_de", "lang_name_es", "lang_name_fr",
		"lang_name_ja", "lang_name_ko", "lang_name_zh", "lang_name_pt",
	}
	for _, locale := range tr.Locales() {
		body, ok := tr.entries[locale]
		if !ok {
			t.Errorf("locale %q missing in entries", locale)
			continue
		}
		for _, key := range expectedKeys {
			if _, ok := body[key]; !ok {
				t.Errorf("locale %q missing key %q", locale, key)
			}
		}
	}
}
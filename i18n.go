// Per user request 2026-07-04: internationalisation (i18n)
// support for the caddy_media_gallery module. The visitor
// can switch between languages via:
//   1. ?lang=<locale>  URL parameter (highest priority)
//   2. <lang> cookie (set by JS when visitor changes lang)
//   3. localStorage 'gallery-language' (set by JS — kept in
//      sync with the cookie so JS-only visitors don't lose
//      their preference on first visit after a server restart)
//   4. Accept-Language request header
//   5. Default language (set by the operator in the Caddyfile
//      via `default_language = "..."`; falls back to "en")
//
// Operator-side: new languages can be added by dropping a
// JSON file at `<templates_dir>/lang/<locale>.json`. No
// rebuild needed — the disk file overrides the embedded
// one (or adds a brand-new locale).
package gallery

import (
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

//go:embed lang/*.json
var embeddedLangFS embed.FS

// langEntry holds the parsed JSON content for one locale.
type langEntry struct {
	locale string
	body   map[string]string
}

// Translator resolves translation keys to strings for the
// given locale. Constructed once per Gallery (at Provision
// time) and shared across all ServeHTTP calls (read-only
// after construction). The disk-override layer is consulted
// at construction; embedded defaults are layered on top so
// any key missing in the disk file falls through to the
// embedded value (or to "en" if neither has it).
type Translator struct {
	mu sync.RWMutex // guards diskCache (operator can refresh via SIGHUP-style admin)
	// locales is the sorted list of supported locales (e.g.
	// ["en", "es", "fr", "de", "ja", "ko", "pt", "zh"]).
	locales []string
	// entries holds the merged translations per locale. The
	// fallback chain is: requested locale -> "en" -> key
	// itself (so a missing key shows something visible in
	// logs but never crashes the page render).
	entries map[string]map[string]string
	// diskDir is the directory to scan for operator-supplied
	// language files (typically /etc/caddy/gallery-templates/lang).
	// Empty = no disk overrides.
	diskDir string
	// diskCache tracks which locale files have been loaded
	// from disk (and their mtimes) so we don't re-read on
	// every request. Mutated under mu.
	diskCache map[string]os.FileInfo
}

// NewTranslator builds a Translator from the embedded lang/
// directory plus an optional disk-override directory.
//
// diskDir may be empty (no overrides). Operator-supplied
// translations in diskDir override the embedded ones, OR
// add brand-new locales that weren't embedded.
func NewTranslator(diskDir string) (*Translator, error) {
	t := &Translator{
		entries:   make(map[string]map[string]string),
		diskDir:   diskDir,
		diskCache: make(map[string]os.FileInfo),
	}
	// Load embedded defaults first (lower priority).
	embedded, err := loadEmbeddedLangs()
	if err != nil {
		return nil, fmt.Errorf("load embedded langs: %w", err)
	}
	for locale, body := range embedded {
		t.entries[locale] = body
	}
	// Then overlay disk files (higher priority).
	if diskDir != "" {
		if err := t.refreshDiskOverrides(); err != nil {
			// Non-fatal: log and continue with embedded only.
			log.Printf("i18n: disk override scan failed: %v (continuing with embedded only)", err)
		}
	}
	// Build sorted locale list.
	t.locales = make([]string, 0, len(t.entries))
	for locale := range t.entries {
		t.locales = append(t.locales, locale)
	}
	sort.Strings(t.locales)
	// Sanity check: "en" must always be present (fallback).
	if _, ok := t.entries["en"]; !ok {
		return nil, fmt.Errorf("i18n: English (en) translations missing — embedded defaults corrupted")
	}
	return t, nil
}

// loadEmbeddedLangs parses every JSON file in the embedded
// lang/ directory. Each file must be a flat key→string map.
// The _meta key (if present) is treated as metadata and
// discarded from the lookup table.
func loadEmbeddedLangs() (map[string]map[string]string, error) {
	out := make(map[string]map[string]string)
	entries, err := embeddedLangFS.ReadDir("lang")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		locale := strings.TrimSuffix(e.Name(), ".json")
		// Normalise: lowercase, accept any casing.
		locale = strings.ToLower(locale)
		data, err := embeddedLangFS.ReadFile("lang/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		body, err := parseLangJSON(data)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		out[locale] = body
	}
	return out, nil
}

// parseLangJSON parses a translation file. Flat key→string
// map. The optional "_meta" key is discarded (used for
// metadata like locale name, translator comments).
func parseLangJSON(data []byte) (map[string]string, error) {
	// Two-pass parse: first into a generic map so we can
	// extract _meta, then re-parse into the flat string
	// table. This keeps the JSON shape flexible (future
	// metadata fields can be added without breaking the
	// loader) while giving the lookup a clean type.
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if k == "_meta" {
			continue
		}
		s, ok := v.(string)
		if !ok {
			// Skip non-string values rather than failing —
			// the embedded defaults are curated, but an
			// operator-supplied file might have a
			// malformed value for one key without breaking
			// the whole locale.
			continue
		}
		out[k] = s
	}
	return out, nil
}

// refreshDiskOverrides re-scans diskDir for <locale>.json
// files and overlays them onto t.entries. Called at
// construction and at request time (cheap mtime check
// first, full reload only when mtime changes).
func (t *Translator) refreshDiskOverrides() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.diskDir == "" {
		return nil
	}
	entries, err := os.ReadDir(t.diskDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // disk dir doesn't exist — not an error
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		fullPath := filepath.Join(t.diskDir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		// Cheap mtime check: skip if unchanged.
		if cached, ok := t.diskCache[fullPath]; ok {
			if cached.ModTime().Equal(info.ModTime()) && cached.Size() == info.Size() {
				continue
			}
		}
		// (Re)load this file.
		locale := strings.ToLower(strings.TrimSuffix(e.Name(), ".json"))
		data, err := os.ReadFile(fullPath)
		if err != nil {
			log.Printf("i18n: read disk %s: %v (skipping)", fullPath, err)
			continue
		}
		body, err := parseLangJSON(data)
		if err != nil {
			log.Printf("i18n: parse disk %s: %v (skipping)", fullPath, err)
			continue
		}
		// Overlay on top of existing entry (disk takes
		// precedence over embedded for any keys it defines).
		if existing, ok := t.entries[locale]; ok {
			for k, v := range body {
				existing[k] = v
			}
			body = existing
		}
		t.entries[locale] = body
		t.diskCache[fullPath] = info
		// Make sure the locale list includes this one.
		found := false
		for _, l := range t.locales {
			if l == locale {
				found = true
				break
			}
		}
		if !found {
			t.locales = append(t.locales, locale)
			sort.Strings(t.locales)
		}
		log.Printf("i18n: loaded locale %q from %s (keys: %d)", locale, fullPath, len(body))
	}
	return nil
}

// T looks up a translation key in the given locale and
// returns the formatted string. The fallback chain is:
//   1. Requested locale
//   2. "en" (English — always the canonical fallback)
//   3. The key itself (so a missing key is visible in the
//      rendered page rather than crashing)
//
// Placeholders use Go's fmt syntax: {key}, {name}, {n}, etc.
// If args are supplied, they're substituted via fmt.Sprintf.
// If no args are supplied, the raw string is returned
// (so single-brace text without substitutions just works).
func (t *Translator) T(locale, key string, args ...any) string {
	// Try requested locale first.
	if body, ok := t.entries[locale]; ok {
		if s, ok := body[key]; ok {
			return formatT(s, args)
		}
	}
	// Fall back to English.
	if body, ok := t.entries["en"]; ok {
		if s, ok := body[key]; ok {
			return formatT(s, args)
		}
	}
	// Last resort: return the key (visible in the page).
	return key
}

// formatT applies substitution if there are args, otherwise
// returns the string unchanged. Substitution uses named
// placeholders of the form {name}, supplied as positional
// arguments (the i-th arg fills the i-th placeholder).
// Example: T("en", "page_of", 2, 5) → "Page 2 of 5".
//
// We use strings.Replace rather than fmt.Sprintf so that
// translators can use the natural {name} syntax without
// worrying about Go's printf verbs (%v, %d, etc.).
func formatT(s string, args []any) string {
	if len(args) == 0 {
		return s
	}
	out := s
	for _, a := range args {
		// Find the first {n} placeholder and replace it
		// with the string form of a. We don't validate
		// the placeholder name — anything {x} works.
		start := strings.Index(out, "{")
		if start < 0 {
			break
		}
		end := strings.Index(out[start:], "}")
		if end < 0 {
			break
		}
		out = out[:start] + toString(a) + out[start+end+1:]
	}
	return out
}

// toString converts any value to its display string. We
// avoid fmt.Sprintf("%v", v) for booleans (which produces
// "true"/"false") — that's fine for our use case. Numbers
// use Go's default formatting.
func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	default:
		return fmt.Sprintf("%v", x)
	}
}

// Locales returns the sorted list of supported locales.
func (t *Translator) Locales() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, len(t.locales))
	copy(out, t.locales)
	return out
}

// tr is a package-level helper that looks up a translation
// key using the currently-active translator + locale. Set
// per-render by RenderPage via tMu. Returns the key itself
// if the translator or locale is unset (defensive — should
// not happen in production but might in unit tests).
//
// Used by Go code that needs to produce translated strings
// outside the template engine (e.g. computeFilterGroups
// building the filter dropdown labels in render.go). The
// template's "t" function (in render.go's galleryFuncs) is
// the SAME thing, just for templates. Both read currentT +
// currentLang under tMu.
func tr(key string) string {
	tMu.RLock()
	translator := currentT
	lang := currentLang
	tMu.RUnlock()
	if translator == nil {
		return key
	}
	if lang == "" {
		lang = "en"
	}
	return translator.T(lang, key)
}

// NativeName returns the native-script name of a locale
// (e.g. "English" for "en", "Deutsch" for "de", "日本語"
// for "ja"). The name is ALWAYS rendered in the locale's
// OWN language — we read from the requested locale's
// translation file, not from "en". So when the visitor is
// viewing the page in Japanese, the options show their
// native names (英語, ドイツ語, etc.) because
// each locale's JSON file has the native name of every
// other locale translated.
//
// Falls back to the locale code itself if the native name
// isn't found (e.g. a new locale was added but the native
// name wasn't populated in any JSON file).
//
// Per user request 2026-07-04: the dropdown UI shows
// native script names regardless of the visitor's current
// locale — the visitor's eye picks out their language from
// the dropdown instantly.
func (t *Translator) NativeName(locale string) string {
	// First try the locale's own translation of its own
	// name. "lang_name_en" in en.json is "English";
	// "lang_name_en" in de.json is "Englisch"; etc.
	key := "lang_name_" + locale
	if body, ok := t.entries[locale]; ok {
		if name, ok := body[key]; ok && name != "" {
			return name
		}
	}
	// Fall back to any other locale that has this name.
	for _, body := range t.entries {
		if name, ok := body[key]; ok && name != "" {
			return name
		}
	}
	// Last resort: return the locale code itself.
	return locale
}

// SelfName returns the native name of the translator's
// own locale (the key used for the current locale trigger
// button). Equivalent to NativeName(locale) but reads
// directly without the locale arg — callers don't need
// to track the current locale separately.
func (t *Translator) SelfName(locale string) string {
	return t.NativeName(locale)
}

// HasLocale reports whether the given locale is supported.
func (t *Translator) HasLocale(locale string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	locale = strings.ToLower(locale)
	for _, l := range t.locales {
		if l == locale {
			return true
		}
	}
	return false
}

// DetectLocale resolves the visitor's preferred locale using
// the priority order documented at the top of this file:
//   1. ?lang=<locale>  URL parameter
//   2. <lang> cookie
//   3. localStorage 'gallery-language' header (NOT handled
//      here — localStorage is client-side only. The cookie
//      layer (item 2) is the server-readable proxy for it)
//   4. Accept-Language request header
//   5. operator default (from Caddyfile `default_language`)
//   6. "en"
//
// Returns the best match (in canonical short form, e.g.
// "de" not "de-de"). If the visitor's preference is for a
// locale we don't support, fall back to the operator default
// (then to "en").
func DetectLocale(r *http.Request, supported []string, defaultLocale string) string {
	if defaultLocale == "" {
		defaultLocale = "en"
	}
	// 1. URL parameter (highest priority — visitor explicitly chose).
	if lang := r.URL.Query().Get("lang"); lang != "" {
		if matchLocale(lang, supported) {
			return stripRegion(lang, supported)
		}
	}
	// 2. Cookie (set by JS after the visitor picks).
	if c, err := r.Cookie("gallery-language"); err == nil {
		if matchLocale(c.Value, supported) {
			return stripRegion(c.Value, supported)
		}
	}
	// 3. Accept-Language header (browser preference).
	if al := r.Header.Get("Accept-Language"); al != "" {
		if best := pickFromAcceptLanguage(al, supported); best != "" {
			return best
		}
	}
	// 4. Operator default.
	if matchLocale(defaultLocale, supported) {
		return stripRegion(defaultLocale, supported)
	}
	// 5. Last resort: first supported locale (alphabetical),
	//    or "en" if for some reason that's not available.
	if len(supported) > 0 {
		return strings.ToLower(supported[0])
	}
	return "en"
}

// matchLocale checks if `requested` matches any of the
// supported locales (exact, or stripped of region suffix).
// For example: "de-DE" matches "de".
func matchLocale(requested string, supported []string) bool {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested == "" {
		return false
	}
	for _, s := range supported {
		s = strings.ToLower(s)
		if requested == s {
			return true
		}
		// Strip region: "de-de" -> "de".
		if strings.HasPrefix(requested, s+"-") {
			return true
		}
		// Also try matching the other direction: "de" matches "de-de".
		if strings.HasPrefix(s, requested+"-") {
			return true
		}
	}
	return false
}

// pickFromAcceptLanguage parses a raw Accept-Language header
// and returns the best matching supported locale. The
// quality factor (q=) is honoured. Returns "" if nothing
// matches (caller falls back to default).
//
// Example header: "de-DE,de;q=0.9,en;q=0.8"
func pickFromAcceptLanguage(header string, supported []string) string {
	type pref struct {
		tag    string
		weight float64
	}
	var prefs []pref
	for _, raw := range strings.Split(header, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		parts := strings.Split(raw, ";")
		tag := strings.ToLower(strings.TrimSpace(parts[0]))
		weight := 1.0
		for _, p := range parts[1:] {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "q=") {
				var w float64
				if _, err := fmt.Sscanf(p[2:], "%f", &w); err == nil {
					weight = w
				}
			}
		}
		prefs = append(prefs, pref{tag: tag, weight: weight})
	}
	// Sort by weight descending.
	sort.SliceStable(prefs, func(i, j int) bool {
		return prefs[i].weight > prefs[j].weight
	})
	for _, p := range prefs {
		if matchLocale(p.tag, supported) {
			// Return the canonical short form, not the
			// full tag (e.g. "de" not "de-de").
			return stripRegion(p.tag, supported)
		}
	}
	return ""
}

// stripRegion returns the canonical short form of a locale
// tag by finding the matching supported locale. For example,
// "de-de" returns "de".
func stripRegion(tag string, supported []string) string {
	tag = strings.ToLower(tag)
	for _, s := range supported {
		s = strings.ToLower(s)
		if tag == s || strings.HasPrefix(tag, s+"-") || strings.HasPrefix(s, tag+"-") {
			return s
		}
	}
	return tag
}
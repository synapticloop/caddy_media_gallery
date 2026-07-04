# Feature: internationalisation (i18n)

Per user request 2026-07-04: the gallery now supports 8
locales out of the box — English, German, Spanish,
French, Japanese, Korean, Chinese, Portuguese.
Visitors pick their preferred language from a dropdown
in the header; the selection is remembered across visits.

## What gets translated

| Surface | Examples |
|---|---|
| Header title row | "images" (no translation — directory names are path data, not UI text) |
| Header status row | "47 files / 36 images · 8 videos · 3 other files / (22.5 MB total) / 40 directories" |
| Show-pages dropdown | "Show 60 Per page" / "Anzeigen 60 Pro Seite" / "表示 60 表示件数" |
| Filter dropdown labels | "Images" / "Bilder" / "画像" |
| Directories table headers | "# Files" / "# Dateien" / "# ファイル" |
| Pagination buttons | "← Prev" / "← Zurück" / "← 前へ" |
| Show-pages "all" option | "all" / "Alle" / "すべて" |
| Media section header | "Media (44 - Showing 1-44)" / "メディア (44 - 表示中 1-44)" |
| Media section header (search) | "Media (44 - search 'img' - showing 0 of 44)" |
| Lightbox EXIF panel | "Camera", "Lens", "Date", "Exposure" |
| Lightbox META panel | "Duration", "Container", "Video", "Audio", "Bitrate", "Framerate" |
| Lightbox controls | "Open in new tab", "Close", "Previous", "Next" |

Everything that is **dynamic data** (filenames, dates,
sizes, dimensions, file counts) stays in its raw form —
those aren't locale-dependent.

## Locale resolution priority chain

When the server receives a request, it resolves the
visitor's locale in this order (highest priority first):

```
1. ?lang=<locale>       URL parameter (set by clicking the picker)
2. gallery-language      cookie (set by JS on first dropdown click)
3. localStorage          (read by JS on next page load, synced to cookie)
4. Accept-Language       HTTP request header (from browser settings)
5. default_language      Caddyfile directive (operator-configured)
6. en                    hardcoded fallback (lowest)
```

This chain is implemented in `i18n.go` `DetectLocale()`,
called by `gallery.go` `ServeHTTP()` before `RenderPage()`.
The resolved locale is passed into the template via
`PageData.Locale` and used to set the `<html lang="...">`
attribute.

## How the visitor picks a language

1. Visitor clicks the language dropdown trigger (shows
   current locale name in their language, e.g. "English",
   "Deutsch", "日本語") in the header.
2. Dropdown opens showing all 8 locales by their native
   names (always "English", "Deutsch", "日本語", etc. —
   so each visitor can recognise their own language
   regardless of the current locale).
3. Visitor picks a locale → JS navigates to `?lang=<locale>`
   (a full page reload — by design, per the user's "only
   one reload" requirement).
4. Server resolves the locale (URL > cookie > localStorage >
   Accept-Language > default_language > en) and renders
   the page in that locale.
5. JS reads `?lang=` from the URL → writes to
   `localStorage["gallery-language"]` AND sets a
   `gallery-language` cookie (1-year expiry).
6. On every subsequent visit, the cookie is sent
   automatically — the page renders in the chosen locale
   on the FIRST request, with NO further reloads needed.

If the visitor clears cookies (or uses a different
browser), the `Accept-Language` header is used as the
fallback (q-factor matched against the supported locales).
If that's also missing, the `default_language` Caddyfile
directive is used. If that's not set, `en` is the last
resort.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│ Server side (Go)                                            │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  i18n.go                                                    │
│  ┌─────────────┐                                            │
│  │ Translator  │ ──── holds all 8 locale maps               │
│  │             │      en, de, es, fr, ja, ko, zh, pt        │
│  └─────────────┘                                            │
│        │                                                    │
│        ├── T(key) → string                                 │
│        ├── NativeName(locale) → string                     │
│        └── SelfName() → string                              │
│                                                             │
│  NewTranslator(embedFS, diskDir) → *Translator            │
│    └─ loads lang/*.json (embedded)                         │
│       + /etc/caddy/gallery-templates/lang/*.json (override) │
│                                                             │
│  DetectLocale(r, defaultLang) → string                     │
│    └─ priority chain: URL > cookie > ...                   │
│                                                             │
│  package-level:                                             │
│  ┌─────────────────┐    ┌──────────────────┐              │
│  │ currentT        │    │ currentLang      │              │
│  │ *Translator     │    │ string            │              │
│  └─────────────────┘    └──────────────────┘              │
│        ▲                       ▲                            │
│        │ tMu (RWMutex)         │                            │
│                                                             │
│  RenderPage() sets these on entry, restores via defer.       │
│                                                             │
│  tr(key) — package-level Go helper                          │
│  {{t "key"}} — template function (defined in galleryFuncs)  │
│  {{tr.NativeName "ja"}} — template helper for names       │
│                                                             │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│ Client side (JS)                                            │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  // Server-rendered into the page:                         │
│  var TRANSLATIONS = {                                       │
│    media_label: '{{t "media_label"}}',                     │
│    search_label: '{{t "search_label"}}',                   │
│    ...                                                      │
│  };                                                         │
│                                                             │
│  function t(key) {                                          │
│    return TRANSLATIONS[key] || key;                         │
│  }                                                          │
│                                                             │
│  // Used by:                                                │
│  //  - lightbox panel headers (EXIF/META field labels)      │
│  //  - search header JS (Media (N - search 'q' - ...))     │
│                                                             │
│  // Read localStorage on page load, sync to cookie          │
│  // if missing (so the server sees it on next request).    │
│  // Also handle URL ?lang= → write to both localStorage     │
│  // AND cookie (so subsequent visits use cookie directly).  │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

## Operator: adding a new locale

Operators can add a new locale **without rebuilding Caddy**:

1. Create `/etc/caddy/gallery-templates/lang/<locale>.json`
   with the same keys as `lang/en.json`. Copy `en.json`
   first, then translate each value. The `_meta` block at
   the bottom should also be translated (it's used for
   locale names shown in the picker).

2. Restart Caddy (or just wait for the next request —
   the disk override is read on each `NewTranslator()` call).

3. The new locale appears in the dropdown automatically
   (the picker reads `Translator.Locales()` which includes
   the new file).

### Example: adding Italian (`it`)

```bash
# Copy English as a template
sudo cp /etc/caddy/gallery-templates/lang/en.json \
        /etc/caddy/gallery-templates/lang/it.json

# Edit it.json — translate each value
sudo vim /etc/caddy/gallery-templates/lang/it.json
```

After this, visitors see "Italiano" in the language
picker. No Caddy rebuild required.

### Translating the `_meta` block

The `_meta` block at the bottom of each lang file holds
the **native-script names of all locales** — used by the
picker so visitors can recognise their own language
regardless of the current locale:

```json
"_meta": {
  "lang_name_en": "English",
  "lang_name_de": "Deutsch",
  "lang_name_es": "Español",
  "lang_name_fr": "Français",
  "lang_name_ja": "日本語",
  "lang_name_ko": "한국어",
  "lang_name_zh": "中文",
  "lang_name_pt": "Português",
  "lang_name_it": "Italiano"
}
```

When adding a new locale, update **all** existing
`_meta` blocks to include `lang_name_<your_locale>` with
the native-script name in that language. This ensures the
new locale shows up correctly in every other locale's
picker.

## Configuration: `default_language`

The `default_language` Caddyfile directive sets the fallback
locale when the visitor hasn't yet specified one (via URL,
cookie, or browser Accept-Language). Defaults to `en`.

```caddy
media_gallery {
    default_language ja   # Japanese, for a mostly Japanese audience
    path_prefix /images/
    root_name images
}
```

If `default_language` is not set, or if the locale string
is not a supported one, the gallery falls back to `en`.

## Locale file format

Each locale is a JSON file with a flat key→string map. The
`_meta` block at the bottom is reserved for locale metadata
(locale names shown in the picker) and is NOT prefixed with
the meta block name (it's accessed via the meta block
directly).

Example (`lang/en.json` excerpt):

```json
{
  "page_prev_arrow": "← Prev",
  "page_next_arrow": "Next →",
  "files_singular": "file",
  "files_plural": "files",
  "images_count": "images",
  "media_label": "Media",
  "search_label": "search",
  ...
  "_meta": {
    "lang_name_en": "English",
    "lang_name_de": "Deutsch",
    ...
  }
}
```

## Adding new translation keys

When adding a new English string to the template, you must
also add the same key to all 8 lang files. The simplest
workflow:

1. Add the key + English value to `lang/en.json`
2. Add the key + translation to `lang/de.json`,
   `lang/es.json`, etc.
3. Use `{{t "your_new_key"}}` in the template
4. Restart Caddy (the lang files are read on each request
   via the disk override)

If a key is missing in a non-English locale, the English
fallback is used. So you can ship an untranslated key in
all 8 files first, then translate one locale at a time
without breaking anything.

## Testing

The i18n feature has 18 unit tests + 11 visual test groups:

- **Unit tests** (`i18n_test.go`, `i18n_render_test.go`,
  `i18n_native_name_test.go`): translator lookup, locale
  resolution, embed/disk-override behavior, fallback
  chain.

- **RenderPage tests** (`i18n_render_test.go`, existing
  tests updated): every page-rendering test now produces
  English output via `TestMain`'s package-level
  translator setup.

- **Visual tests** (`tests/test_i18n_visual.py` +
  `i18n_visual_test.go`): Playwright loads the page in
  each of the 8 languages and verifies:
  - `<html lang="...">` is set correctly
  - Language picker trigger shows the current locale name
  - All 8 native-script locale names appear in the
    dropdown
  - Active option is correctly highlighted
  - Sort label and back-to-top button are translated

Run the visual tests:
```bash
# Via Python directly
python3 tests/test_i18n_visual.py

# Via Go (with the visual build tag)
go test -tags=visual -run TestI18nVisual ./...
```

## URL parameters

| Param | Effect | Example |
|---|---|---|
| `?lang=<locale>` | Sets the locale (highest priority) | `?lang=de` |
| `?fresh=<timestamp>` | Bypasses any HTTP cache (test/dev convenience) | `?fresh=1234567890` |

`?lang=` is the only locale-relevant parameter. All other
URL params (sort, filter, pagination, search) are
locale-agnostic and work the same way regardless of the
active language.

## Browser compatibility

The locale picker uses native `<details>`/`<summary>` for
the dropdown — no custom JS required, keyboard
navigation built-in, screen-reader friendly. Works in all
evergreen browsers (Chrome, Firefox, Safari, Edge).
# Visual (Playwright) tests

Per user request 2026-07-04: visual tests for the
internationalisation feature. These tests load the live
gallery page in different languages via Playwright and
verify the translated strings appear correctly.

## What's tested

For each of the 8 supported locales (`en`, `de`, `es`,
`fr`, `ja`, `ko`, `zh`, `pt`):

- The `<html lang="...">` attribute is set correctly
- The language picker trigger shows the current locale's
  native name (e.g. "Deutsch" for de, "日本語" for ja)
- The picker dropdown shows all 8 native-script locale
  names regardless of the current locale
- The currently-active option is correctly highlighted
- The sort label is translated
- The back-to-top button is translated

Plus a default-locale test: with no `?lang=` parameter,
the page should render in English.

## Running

### Direct (Python)

```bash
# One-time setup
pip install playwright
playwright install chromium

# Run the tests
python3 tests/test_i18n_visual.py
```

Expected output:

```
============================================================
i18n visual tests (Playwright)
============================================================

=== Testing locale: en ===
  ✓ <html lang>: 'en'
  ✓ Picker trigger: 'English'
  ...
11/11 test groups passed
```

### Via Go

The Go test wrapper (`i18n_visual_test.go`) is tagged
with `//go:build visual` so it doesn't run with the
default `go test ./...` (which is what CI uses).

```bash
# Standard run (visual tests excluded)
go test ./...

# Include visual tests
go test -tags=visual -run TestI18nVisual ./...
```

The Go wrapper invokes the Python script as a subprocess
and fails the test if the script exits non-zero.

## What's NOT tested

- The language picker dropdown is NOT clicked to verify
  the localStorage/cookie persistence flow (that would
  require more complex setup and is verified manually).
- The page size form has a pre-existing bug (the form
  action is set to the current URL with the `?fresh=`
  cache buster, so the form submits to itself without
  actually changing the page size). This is NOT a
  regression from i18n — it's unrelated and should be
  fixed in a separate commit.
- Dark/light theme toggle is NOT tested (no visual
  regression risk — the existing tests cover that).

## Adding new test cases

To add a new test:

1. Add a new function in `tests/test_i18n_visual.py`
   (e.g. `test_my_feature`).
2. Add it to the `results` list in `main()`.
3. Re-run `python3 tests/test_i18n_visual.py` to verify.

The test helpers in the file (`fresh_page`, `expect`,
`get_*`) are designed to be reusable. Prefer adding
expected translations to the `EXPECTED_LOCALES` dict
or to a per-test `expected = {...}` dict rather than
hardcoding strings in multiple places.

## Prerequisites

- Python 3.x
- `playwright` Python package
- Chromium browser (installed via `playwright install`)
- Network access to `https://hermes.synapticloop.com/`
- The live Caddy server must be running with the
  `internationalisation` branch built in
  (commit `bdb661d` or later).
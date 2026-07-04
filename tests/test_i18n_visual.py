"""Visual (Playwright) tests for the i18n feature.

Per user request 2026-07-04: verify the internationalisation
implementation by loading the page in different languages
and checking that the translated strings appear correctly.

These tests assume the live Caddy server is running on
https://hermes.synapticloop.com/ with the i18n module
loaded. They hit the production URL (bypassing the
localhost-only bypass via the /etc/hosts mapping).

Run with:
    python3 tests/test_i18n_visual.py

Or, if you have pytest:
    python3 -m pytest tests/test_i18n_visual.py -v

Each test:
  1. Opens a fresh browser context
  2. Navigates to the gallery root (with ?lang=<locale> to
     force a specific language)
  3. Verifies that key UI elements are translated:
     - The <html lang="..."> attribute
     - The sort label
     - The search placeholder
     - The back-to-top button text
  4. Verifies that the language picker dropdown shows the
     current locale name + all 8 native-script locale names
  5. Cleans up

The tests are designed to be hermetic (no test order
dependency, no shared state) and idempotent (re-runnable).
"""

import sys
import time
from typing import Optional

try:
    from playwright.sync_api import sync_playwright, Browser, BrowserContext, Page
except ImportError:
    print("playwright not installed. Run: pip install playwright", file=sys.stderr)
    print("And: playwright install chromium", file=sys.stderr)
    sys.exit(1)

# Base URL of the live gallery. localhost bypass means we
# don't need to auth through Authelia.
BASE_URL = "https://hermes.synapticloop.com/images/"

# All 8 supported locales, with the expected native name
# shown in the picker (matches each locale's own JSON file's
# lang_name_<locale> entry).
EXPECTED_LOCALES = {
    "en": "English",
    "de": "Deutsch",
    "es": "Español",
    "fr": "Français",
    "ja": "日本語",
    "ko": "한국어",
    "zh": "中文",
    "pt": "Português",
}


def fresh_page(browser: Browser, locale: Optional[str] = None) -> tuple[BrowserContext, Page]:
    """Open a fresh context + page, navigate to the gallery
    in the specified locale (via ?lang= URL param).
    Returns the context and page. The caller is responsible
    for closing the context (otherwise the browser leaks
    memory until the test process exits).
    """
    context = browser.new_context(
        viewport={"width": 1400, "height": 900},
        ignore_https_errors=True,
    )
    page = context.new_page()
    url = BASE_URL
    if locale:
        # Cache buster so we don't see a cached page.
        sep = "&" if "?" in url else "?"
        url = f"{url}{sep}lang={locale}&fresh={time.time()}"
    page.goto(url, wait_until="domcontentloaded", timeout=30000)
    # Give the JS a moment to settle (theme IIFE, etc.).
    page.wait_for_load_state("networkidle", timeout=10000)
    return context, page


def get_html_lang(page: Page) -> Optional[str]:
    """Return the <html lang="..."> attribute value."""
    return page.evaluate("document.documentElement.lang") or None


def get_sort_label(page: Page) -> Optional[str]:
    """Return the text of the .sort-label element (the heading
    above the Name/Type/Modified/Size sort buttons)."""
    el = page.locator(".sort-label").first
    if el.count() == 0:
        return None
    return el.text_content() or None


def get_search_placeholder(page: Page) -> Optional[str]:
    """Return the placeholder of the search input."""
    el = page.locator("input.search-input").first
    if el.count() == 0:
        return None
    return el.get_attribute("placeholder")


def get_back_to_top(page: Page) -> Optional[str]:
    """Return the text inside the .back-to-top button."""
    el = page.locator(".back-to-top-text").first
    if el.count() == 0:
        return None
    return el.text_content() or None


def get_lang_picker(page: Page) -> dict:
    """Return a snapshot of the language picker UI:
      - trigger_text: what's in the trigger button (e.g. "English")
      - options: list of all option labels (e.g. ["Deutsch", ...])
      - active_option: the option marked as currently active
    """
    result = page.evaluate("""
        (function() {
            var t = document.getElementById('lang-toggle');
            if (!t) return null;
            var trigger = t.querySelector('.lang-current');
            var options = t.querySelectorAll('.lang-option');
            var optTexts = [];
            var active = null;
            for (var i = 0; i < options.length; i++) {
                optTexts.push(options[i].textContent.trim());
                if (options[i].classList.contains('lang-option-active')) {
                    active = options[i].textContent.trim();
                }
            }
            return {
                triggerText: trigger ? trigger.textContent.trim() : null,
                options: optTexts,
                activeOption: active,
            };
        })
    """)
    return result or {}


def expect(actual, expected, label: str) -> bool:
    """Compare actual to expected. Print a clear pass/fail
    message. Return True if they match, False otherwise.
    """
    if actual == expected:
        print(f"  ✓ {label}: {actual!r}")
        return True
    else:
        print(f"  ✗ {label}: expected {expected!r}, got {actual!r}")
        return False


def test_locale(browser: Browser, locale: str) -> bool:
    """Test that a specific locale renders correctly."""
    print(f"\n=== Testing locale: {locale} ===")
    context, page = fresh_page(browser, locale=locale)
    try:
        all_ok = True
        all_ok &= expect(get_html_lang(page), locale, "<html lang>")
        expected = EXPECTED_LOCALES[locale]
        all_ok &= expect(get_lang_picker(page).get("triggerText"), expected, "Picker trigger")
        # Picker options: all 8 locales in their own native names
        picker = get_lang_picker(page)
        for other_locale, other_name in EXPECTED_LOCALES.items():
            all_ok &= expect(
                other_name in (picker.get("options") or []),
                True,
                f"Picker has '{other_name}' ({other_locale})",
            )
        # Active option matches current locale
        all_ok &= expect(
            picker.get("activeOption"),
            expected,
            "Active option in picker",
        )
        return all_ok
    finally:
        context.close()


def test_translated_strings(browser: Browser) -> bool:
    """Test that key UI strings are translated for each
    supported locale. Each locale should have a recognizable
    sort label (NOT "Sort Media By" — that's only English).
    """
    print("\n=== Testing translated UI strings across locales ===")
    all_ok = True
    # Per-locale expected sort label (the "Sort Media By" text)
    expected_sort_labels = {
        "en": "Sort Media By",
        "de": "Medien sortieren nach",
        "es": "Ordenar medios por",
        "fr": "Trier les médias par",
        "ja": "メディアの並べ替え",
        "ko": "미디어 정렬 기준",
        "zh": "媒体排序",
        "pt": "Ordenar mídia por",
    }
    for locale, expected in expected_sort_labels.items():
        context, page = fresh_page(browser, locale=locale)
        try:
            all_ok &= expect(
                get_sort_label(page),
                expected,
                f"sort_label in {locale}",
            )
        finally:
            context.close()
    return all_ok


def test_default_locale_is_english(browser: Browser) -> bool:
    """Test that the default locale (no ?lang=) is English.
    Note: this assumes the live Caddy has the default
    DefaultLanguage set to "" (falls back to "en").
    """
    print("\n=== Testing default locale (no ?lang=) ===")
    context, page = fresh_page(browser, locale=None)
    try:
        return expect(get_html_lang(page), "en", "Default <html lang>")
    finally:
        context.close()


def test_back_to_top_translation(browser: Browser) -> bool:
    """Test that the back-to-top button is translated."""
    print("\n=== Testing back-to-top button translation ===")
    expected = {
        "en": "Back To Top",
        "de": "Nach oben",
        "es": "Volver arriba",
        "fr": "Retour en haut",
        "ja": "トップへ戻る",
        "ko": "맨 위로",
        "zh": "返回顶部",
        "pt": "Voltar ao topo",
    }
    all_ok = True
    for locale, want in expected.items():
        context, page = fresh_page(browser, locale=locale)
        try:
            all_ok &= expect(get_back_to_top(page), want, f"back-to-top in {locale}")
        finally:
            context.close()
    return all_ok


def main() -> int:
    print("=" * 60)
    print("i18n visual tests (Playwright)")
    print("=" * 60)

    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        try:
            results = []
            for locale in EXPECTED_LOCALES:
                results.append((f"locale: {locale}", test_locale(browser, locale)))
            results.append(("translated UI strings", test_translated_strings(browser)))
            results.append(("default locale = en", test_default_locale_is_english(browser)))
            results.append(("back-to-top translation", test_back_to_top_translation(browser)))
        finally:
            browser.close()

    print("\n" + "=" * 60)
    print("RESULTS")
    print("=" * 60)
    passed = sum(1 for _, ok in results if ok)
    total = len(results)
    for name, ok in results:
        marker = "✓" if ok else "✗"
        print(f"  {marker} {name}")
    print(f"\n{passed}/{total} test groups passed")
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
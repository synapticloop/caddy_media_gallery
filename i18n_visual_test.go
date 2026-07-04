// Per user request 2026-07-04: visual tests for the i18n
// feature. These tests use Playwright (via a Python script)
// to load the live page in different languages and verify
// the translated strings appear correctly.
//
// Build/run constraint: this test invokes `python3
// tests/test_i18n_visual.py`, which requires:
//   - Python 3.x
//   - playwright (`pip install playwright`)
//   - chromium browser (`playwright install chromium`)
//   - The live gallery running at hermes.synapticloop.com
//
// The tests are tagged with `//go:build visual` so they
// don't run with the standard `go test ./...` (which the
// CI / local dev loop uses). To run them explicitly:
//
//   go test -tags=visual -run TestI18nVisual ./...
//
// Or just run the Python script directly:
//
//   python3 tests/test_i18n_visual.py

//go:build visual
// +build visual

package gallery

import (
	"os"
	"os/exec"
	"testing"
)

// TestI18nVisual runs the Playwright visual tests for
// the i18n feature. Per user request 2026-07-04, this
// verifies that:
//   - The <html lang="..."> attribute is set correctly
//   - The sort label is translated in all 8 locales
//   - The back-to-top button is translated in all 8 locales
//   - The language picker dropdown shows the correct
//     native names in all 8 locales
//   - The default locale (no ?lang=) is English
//
// The Python script is invoked as a subprocess; any
// failure (non-zero exit) is reported as a Go test
// failure.
func TestI18nVisual(t *testing.T) {
	// Locate the Python script. We use the test's working
	// directory as the base (assumed to be the project root
	// when `go test` is run from there).
	scriptPath := "tests/test_i18n_visual.py"
	if _, err := os.Stat(scriptPath); err != nil {
		t.Skipf("visual test script not found at %s (skipping; run from project root): %v", scriptPath, err)
	}
	cmd := exec.Command("python3", scriptPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("i18n visual tests failed: %v", err)
	}
}
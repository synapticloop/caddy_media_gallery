# Templates: building the PDF locally

## Building the PDF locally

The PDF book (`caddy-media-gallery-book.pdf`) is built from the
markdown files in `docs/` via pandoc + xelatex. The full command
is wrapped in a script at the project root: `build-docs.sh`.

**To rebuild the PDF locally:**

```bash
./build-docs.sh
```

The script:
- Works from any directory (uses `BASH_SOURCE` to find its
  own location and the project root)
- Verifies prerequisites (`pandoc`, `xelatex`) before running
- Verifies all source files exist before running
- Runs pandoc with the right flags (`--pdf-engine=xelatex`,
  `--include-in-header=preamble.tex`, `--listings`)
- Cleans up intermediate files (`*.aux`, `*.log`, etc.) after
- Prints summary info (output path, file size, page count)

**Prerequisites:**
- `pandoc`
- `xelatex` (usually in the `texlive-xetex` package)
- Install on Debian/Ubuntu:
  `sudo apt-get install pandoc texlive-xetex texlive-fonts-recommended`
- Install on macOS:
  `brew install pandoc mactex`

**Why a script instead of running pandoc directly:**
the pandoc command has several flags (`--include-in-header=preamble.tex`
references the font config; `--listings` is needed for code block
formatting; the source file order matters). The script encapsulates
all of this so you don't have to remember the incantation.

The font config (`preamble.tex` + `docs/fonts/`) references absolute
paths for `fontspec`'s `Path =` option. If you move this project
to another machine, update the `Path =` line in `preamble.tex`
to match the new location.

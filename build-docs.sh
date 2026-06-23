#!/usr/bin/env bash
# build-docs.sh — Regenerate the operator-facing PDF (caddy-media-gallery-book.pdf)
# from the docs/*.md sources.
#
# Can be run from anywhere (uses BASH_SOURCE to find its own location).
# No sudo required — writes only to the project root.
#
# Prerequisites:
#   - pandoc
#   - xelatex (usually in the texlive-xetex package)
#
# Install on Debian/Ubuntu:
#   apt-get install pandoc texlive-xetex texlive-fonts-recommended
# Install on macOS:
#   brew install pandoc mactex

set -euo pipefail

# Locate the project root (the directory containing this script)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$SCRIPT_DIR"
DOCS_DIR="$PROJECT_ROOT/docs"
OUTPUT="$PROJECT_ROOT/caddy-media-gallery-book.pdf"

# Verify prerequisites
MISSING=()
for cmd in pandoc xelatex; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
        MISSING+=("$cmd")
    fi
done
if [ ${#MISSING[@]} -gt 0 ]; then
    echo "ERROR: missing required tools: ${MISSING[*]}" >&2
    echo "" >&2
    echo "Install on Debian/Ubuntu:" >&2
    echo "  sudo apt-get install pandoc texlive-xetex texlive-fonts-recommended" >&2
    echo "Install on macOS:" >&2
    echo "  brew install pandoc mactex" >&2
    exit 1
fi

# Verify the docs directory and its sources exist
if [ ! -d "$DOCS_DIR" ]; then
    echo "ERROR: docs directory not found: $DOCS_DIR" >&2
    exit 1
fi
SOURCES=(
    "$DOCS_DIR/00-cover.md"
    "$DOCS_DIR/01-configuration.md"
    "$DOCS_DIR/02-configuration-reference.md"
    "$DOCS_DIR/03-templates.md"
    "$DOCS_DIR/04-sort-and-pagination.md"
)
MISSING_SOURCES=()
for src in "${SOURCES[@]}"; do
    if [ ! -f "$src" ]; then
        MISSING_SOURCES+=("$src")
    fi
done
if [ ${#MISSING_SOURCES[@]} -gt 0 ]; then
    echo "ERROR: missing source files:" >&2
    printf '  - %s\n' "${MISSING_SOURCES[@]}" >&2
    exit 1
fi

# Verify the preamble + fonts exist
if [ ! -f "$DOCS_DIR/preamble.tex" ]; then
    echo "ERROR: preamble.tex not found: $DOCS_DIR/preamble.tex" >&2
    exit 1
fi
if [ ! -d "$DOCS_DIR/fonts" ] || [ -z "$(ls -A "$DOCS_DIR/fonts" 2>/dev/null)" ]; then
    echo "ERROR: fonts directory empty or missing: $DOCS_DIR/fonts" >&2
    exit 1
fi

echo "Building $OUTPUT ..."
echo "  sources:    ${#SOURCES[@]} markdown files in docs/"
echo "  engine:     xelatex via pandoc"
echo "  preamble:   docs/preamble.tex (Libre Baskerville + JetBrains Mono)"
echo "  output:     $OUTPUT"

# Run pandoc from the docs directory (preamble.tex uses relative paths)
cd "$DOCS_DIR"
pandoc -t pdf --pdf-engine=xelatex \
    --include-in-header=preamble.tex \
    --listings \
    00-cover.md \
    01-configuration.md \
    02-configuration-reference.md \
    03-templates.md \
    04-sort-and-pagination.md \
    -o "$OUTPUT"

# Clean up intermediate files (already in .gitignore, but good hygiene)
rm -f *.aux *.log *.out *.synctex.gz *.toc missfont.log

echo ""
echo "Built: $OUTPUT"
echo "Size:  $(du -h "$OUTPUT" | cut -f1)"
echo "Pages: $(pdfinfo "$OUTPUT" 2>/dev/null | awk '/^Pages:/ {print $2}')"
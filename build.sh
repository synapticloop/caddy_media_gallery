#!/usr/bin/env bash
# Build a custom Caddy binary with the media_gallery module baked in.
# Pins Caddy to v2.11.4 and the local module path.
#
# Usage:
#   ./build.sh                  # build + install to /usr/local/bin/caddy
#   ./build.sh --check          # build into ./caddy (don't install) for CI
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# Add $HOME/go/bin to PATH so xcaddy is findable. xcaddy
# installs to $GOBIN (default $HOME/go/bin) when you `go install
# github.com/caddyserver/xcaddy/cmd/xcaddy@latest`. Using $HOME
# instead of a hardcoded user path keeps this script portable
# across users.
export PATH="$PATH:$HOME/go/bin"

OUTPUT_BIN="/usr/local/bin/caddy"
LOCAL_BIN="$SCRIPT_DIR/caddy"
CHECK_ONLY=0

for arg in "$@"; do
    case "$arg" in
        --check) CHECK_ONLY=1; OUTPUT_BIN="$LOCAL_BIN" ;;
        *) echo "Unknown arg: $arg" >&2; exit 2 ;;
    esac
done

# Back up the existing Caddy binary if not already backed up
if [ "$CHECK_ONLY" -eq 0 ] && [ ! -f /usr/local/bin/caddy.bak-vanilla-2.11.4 ] && [ -f /usr/local/bin/caddy ]; then
    echo "==> Backing up existing Caddy binary to /usr/local/bin/caddy.bak-vanilla-2.11.4"
    sudo cp /usr/local/bin/caddy /usr/local/bin/caddy.bak-vanilla-2.11.4
fi

# Build the custom Caddy
echo "==> Building Caddy with media_gallery module (this can take 30-90s on a cold cache)..."
xcaddy build \
    --output "$OUTPUT_BIN" \
    --with github.com/caddyserver/caddy@v2.11.4 \
    --with github.com/mholt/caddy-ratelimit \
    --with github.com/synapticloop/caddy_media_gallery=. \

chmod +x "$OUTPUT_BIN"

echo ""
echo "==> Built: $("$OUTPUT_BIN" version)"
echo "==> Module check:"
if "$OUTPUT_BIN" list-modules 2>/dev/null | grep -q media_gallery; then
    "$OUTPUT_BIN" list-modules 2>/dev/null | grep media_gallery
    echo "    OK — module is baked in"
else
    echo "    FAIL — media_gallery module NOT found"
    exit 1
fi

if [ "$CHECK_ONLY" -eq 0 ]; then
    echo ""
    echo "==> Restarting Caddy via systemd..."
    sudo systemctl restart caddy
    sleep 2
    if ss -tlnp | grep -qE ':443|:80'; then
        echo "    Caddy is listening on :443 and :80"
    else
        echo "    WARNING: Caddy is NOT listening — check journalctl -u caddy"
        sudo journalctl -u caddy --no-pager -n 20
        exit 1
    fi
    echo "==> Build + install complete."
    echo "    To verify:  curl -skI https://your.caddy.host/ | head -1"
else
    echo "==> Check build complete (binary at $OUTPUT_BIN, not installed)."
fi

#!/usr/bin/env bash
# Build a custom Caddy binary with the media_gallery module baked in.
# Pins Caddy to v2.11.4 and the local module path.
#
# Usage:
#   ./build.sh                  # build + install to /usr/local/bin/caddy (needs sudo)
#   ./build.sh --check          # build into ./caddy (don't install) for CI
#   ./build.sh --user [PORT]    # build + install into ~/bin/caddy (no sudo) for local dev.
#                               # PORT defaults to 8080 (must be > 1024 to avoid needing root).
#                               # Generates a starter Caddyfile in the project root
#                               # pointing at ~/Pictures (override with CADDY_USER_ROOT env var).
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
USER_BIN="$HOME/bin/caddy"
USER_CADDYFILE="$SCRIPT_DIR/Caddyfile.user"
USER_PORT="${CADDY_USER_PORT:-8080}"
USER_ROOT="${CADDY_USER_ROOT:-$HOME/Pictures}"
CHECK_ONLY=0
USER_MODE=0

# Arg parsing. We support both `--user` (default port) and
# `--user 8080` (custom port). The first form is the easier one to
# type; the second lets you script the build with different ports.
PREVIOUS_ARG=""
for arg in "$@"; do
    case "$arg" in
        --check) CHECK_ONLY=1; OUTPUT_BIN="$LOCAL_BIN" ;;
        --user)  USER_MODE=1;  OUTPUT_BIN="$USER_BIN" ;;
        --user=*) USER_MODE=1; OUTPUT_BIN="$USER_BIN"; USER_PORT="${arg#--user=}" ;;
        --help|-h)
            echo "Usage: $0 [--check | --user [PORT]]"
            echo ""
            echo "  (no flag)    Build + install to /usr/local/bin/caddy (needs sudo for install + restart)"
            echo "  --check      Build into ./caddy without installing (for CI)"
            echo "  --user [PORT]  Build + install into ~/bin/caddy (no sudo); generates Caddyfile.user,"
            echo "               serves CADDY_USER_ROOT (default ~/Pictures) on PORT (default 8080)."
            echo ""
            echo "Env vars (--user only):"
            echo "  CADDY_USER_PORT  Port to listen on (default 8080; must be > 1024 to avoid needing root)"
            echo "  CADDY_USER_ROOT  Directory to serve (default ~/Pictures)"
            exit 0
            ;;
        *)
            # If the previous arg was exactly "--user", treat this as the port
            if [ "$PREVIOUS_ARG" = "--user" ] && [[ "$arg" =~ ^[0-9]+$ ]]; then
                USER_PORT="$arg"
            else
                echo "Unknown arg: $arg (try --help)" >&2
                exit 2
            fi
            ;;
    esac
    PREVIOUS_ARG="$arg"
done

# Validate user mode args
if [ "$USER_MODE" -eq 1 ]; then
    if ! [[ "$USER_PORT" =~ ^[0-9]+$ ]] || [ "$USER_PORT" -lt 1025 ] || [ "$USER_PORT" -gt 65535 ]; then
        echo "ERROR: port must be a number between 1025 and 65535 (got: $USER_PORT)" >&2
        echo "  Ports below 1024 require root, which contradicts --user (no-sudo) mode." >&2
        exit 2
    fi
    if [ ! -d "$USER_ROOT" ]; then
        echo "WARNING: root directory does not exist: $USER_ROOT" >&2
        echo "  Caddy will fail to start. Create it first or set CADDY_USER_ROOT to a real directory." >&2
    fi
fi

# Back up the existing Caddy binary if not already backed up
# (only relevant in system-install mode)
if [ "$CHECK_ONLY" -eq 0 ] && [ "$USER_MODE" -eq 0 ] && [ ! -f /usr/local/bin/caddy.bak-vanilla-2.11.4 ] && [ -f /usr/local/bin/caddy ]; then
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

# Three post-build modes: system install (sudo restart), --check
# (no further action), --user (generate Caddyfile + start locally).

if [ "$CHECK_ONLY" -eq 1 ]; then
    echo "==> Check build complete (binary at $OUTPUT_BIN, not installed)."
    exit 0
fi

if [ "$USER_MODE" -eq 1 ]; then
    # No-sudo local install. Generate a starter Caddyfile if one
    # doesn't already exist.
    if [ ! -f "$USER_CADDYFILE" ]; then
        cat > "$USER_CADDYFILE" <<EOF
# Local-dev Caddyfile generated by build.sh --user.
#
# Listens on http://localhost:$USER_PORT (no TLS, no sudo needed).
# Serves the directory pointed at by CADDY_USER_ROOT ($USER_ROOT).
# The admin API is disabled (no auth in this file = no admin
# endpoint exposed; the default 2019 admin is fine on localhost
# but we turn it off for tidiness).

{
    admin off
}

http://localhost:$USER_PORT {
    root * $USER_ROOT

    handle_path /* {
        media_gallery
        file_server
    }
}
EOF
        echo "==> Wrote starter Caddyfile: $USER_CADDYFILE"
    else
        echo "==> Caddyfile.user already exists, leaving it alone: $USER_CADDYFILE"
    fi

    echo ""
    echo "==> Local install complete."
    echo "    Binary:  $OUTPUT_BIN"
    echo "    Config:  $USER_CADDYFILE"
    echo "    URL:     http://localhost:$USER_PORT"
    echo ""
    echo "    To start Caddy in the foreground (Ctrl+C to stop):"
    echo "      $OUTPUT_BIN run --config $USER_CADDYFILE"
    echo ""
    echo "    Or in the background:"
    echo "      nohup $OUTPUT_BIN run --config $USER_CADDYFILE > ~/caddy.log 2>&1 &"
    echo "      echo \$! > ~/caddy.pid  # save the PID to kill it later"
    echo "      cat ~/caddy.log         # tail the log"
    echo ""
    echo "    To stop the background process:"
    echo "      kill \$(cat ~/caddy.pid)"
    exit 0
fi

# System install: restart Caddy via systemd (requires sudo).
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

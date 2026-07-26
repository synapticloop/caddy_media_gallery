package gallery

// Build-time version metadata. These vars are populated by
// xcaddy via `-ldflags "-X ..."` at release time. At dev time
// (running xcaddy without ldflags), they keep the safe defaults
// below so the gallery still renders correctly — the footer just
// shows "caddy_media_gallery dev // unknown" until a proper build
// sets them.
//
// Maintainer: to set these for a release build, run:
//
//   xcaddy build \
//     --with github.com/caddyserver/caddy@v2.11.4 \
//     --with github.com/synapticloop/caddy_media_gallery@<tag> \
//     --with github.com/mholt/caddy-ratelimit \
//     -ldflags "-X github.com/synapticloop/caddy_media_gallery.Version=<tag> \
//               -X github.com/synapticloop/caddy_media_gallery.Commit=$(git rev-parse --short HEAD)"
//
// (Or via the bundled build script if one is added later.)
//
// Or, if building from a git checkout, the maintainer can
// wrap that in a small bash one-liner that runs `git describe`
// + `git rev-parse --short HEAD` and feeds the values into
// -X flags.
//
// Why both Version and Commit are exposed separately: Version is
// the human-readable release tag (e.g. "1.0.2"); Commit is the
// short git hash (7 chars, e.g. "a1b2c3d") that uniquely
// identifies the exact source tree the binary was built from.
// Together they let visitors see which build is running even
// if a binary lacks a tag or has been built from a non-tagged
// commit (e.g. main vs release/1.0.x).
//
// These are exported (`Version`, `Commit`) because the Go linker
// can only set variables that are package-level AND exported.
// Unexported (lowercase) vars can't be set via -ldflags.
var (
	// Version is the human-readable release tag (e.g. "1.0.2",
	// "1.0.2-rc1", "dev"). Set at xcaddy build time via
	// -ldflags "-X github.com/synapticloop/caddy_media_gallery.Version=...".
	Version = "dev"

	// Commit is the short git hash (7 chars) of the source tree
	// the binary was built from. Set at xcaddy build time via
	// -ldflags "-X github.com/synapticloop/caddy_media_gallery.Commit=$(git rev-parse --short HEAD)".
	// Falls back to "unknown" when built without git metadata.
	Commit = "unknown"
)

// VersionString returns a single-line version summary suitable
// for log lines, headers, or compact UIs. Format:
//
//   "caddy_media_gallery 1.0.2 (commit a1b2c3d)"
//   "caddy_media_gallery dev (commit unknown)"
//
// The trailing "(commit ...)" is wrapped in parentheses so a
// downstream parser can grep for the prefix without matching
// the metadata field.
func VersionString() string {
	return "caddy_media_gallery " + Version + " (commit " + Commit + ")"
}
# Templates

The gallery is rendered by an `html/template` (Go's standard
template engine). The template is bundled in the binary as a
Go string constant (`galleryTemplate` in `render.go`), and on
each Caddy startup the module also writes it to disk at
`/etc/caddy/gallery-templates/gallery.tmpl` (or
`$GALLERY_TEMPLATES_DIR` if set). The on-disk file exists so
operators can read the template without opening the binary, and
so they can override individual templates by editing the file
in place.

This page is the **index** for the template docs. Pick a topic:

- **[03a-structure.md](03a-structure.md)** — How the template loads, what's in the file, and the variables it uses
- **[03b-customization.md](03b-customization.md)** — How to edit the templates (walkthroughs, troubleshooting, upgrading)
- **[03c-feature-dark-mode.md](03c-feature-dark-mode.md)** — Dark mode and the theme toggle
- **[03d-feature-localstorage.md](03d-feature-localstorage.md)** — What the template stores in localStorage
- **[03e-feature-lightbox.md](03e-feature-lightbox.md)** — The lightbox (image + video + audio), its controls, the EXIF/META/AUDIO panels
- **[03f-feature-layout.md](03f-feature-layout.md)** — Header, pagination, footer, section heading, open-in-new-tab button
- **[03g-building-pdf.md](03g-building-pdf.md)** — Building the operator manual PDF locally
- **[03h-feature-i18n.md](03h-feature-i18n.md)** — Internationalisation (8 bundled locales, language picker, locale resolution)
- **[03i-feature-audio.md](03i-feature-audio.md)** — Audio file support (1.1.0+, opt-in via `audio_types`, SVG speaker-icon tile, native `<audio controls>` lightbox)

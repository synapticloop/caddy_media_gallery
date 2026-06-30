package gallery

import _ "embed"

// galleryTemplateFS is the embedded copy of the gallery template.
// The template lives in templates/gallery.tmpl (a regular file in
// the repo) so designers and non-Go developers can edit it without
// touching any Go code. The //go:embed directive bundles the file
// at build time, so the template is always available even if the
// on-disk file (/etc/caddy/gallery-templates/gallery.tmpl) is
// missing or has been deleted.
//
// The on-disk file (if it exists) takes precedence at runtime —
// operators can override the embedded template by editing the
// on-disk file directly. See writeBundledTemplates and loadTemplate
// for the resolution order.
//
// Per the project convention (see docs/03-templates.md), the
// template is a single self-contained file. The HTML, CSS
// (inside <style>), and JS (inside <script>) all live together
// in one .tmpl file. There are no separate style.css or
// lightbox.js files.
//
//go:embed templates/gallery.tmpl
var galleryTemplateFS string

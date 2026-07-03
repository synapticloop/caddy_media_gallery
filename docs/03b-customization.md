# Templates: editing and customization

## Editing the templates — the basics

1. **Find the templates dir.** On this host it's
   `/etc/caddy/gallery-templates/`. The file is `gallery.tmpl`.
2. **Edit the file in place.** The module re-reads it on every
   request, so changes take effect immediately — no Caddy restart
   needed.
3. **Test in a browser.** The next request will use the new
   template. If the template has a parse error, the module will
   return 500 with the parser error in the response body. Fix
   the syntax and try again.
4. **Revert to the bundled version.** If you want to go back to
   what the binary ships with, delete the on-disk file:
   `sudo rm /etc/caddy/gallery-templates/gallery.tmpl`. The next
   request falls back to the bundled constant.

## Walkthrough: change the theme to dark mode

Since the CSS is now inlined in the same file as the HTML, you
edit the `<style>` block directly in `gallery.tmpl` — no second
file to coordinate, no sub-template indirection.

1. Open `/etc/caddy/gallery-templates/gallery.tmpl` in your
   editor.
2. Find the `<style>` block. It's the big CSS block near the
   top of the file, just after `<title>...</title>`. Search for
   `html, body { background: #f3f6f7;` to find the body color.
3. Change the colors inline. The CSS is plain CSS — no
   preprocessing. Most of the theme colors are concentrated in
   the first 50-100 lines.
4. Save the file. The change takes effect on the next request
   (no Caddy restart needed — the module re-reads the on-disk
   template on every `loadTemplate` call).
5. Hard-reload in the browser to bypass the HTML cache.

A common set of swaps for dark mode (find/replace these in the
`<style>` block):

| Light (default) | Dark variant |
|---|---|
| `html, body { background: #f3f6f7; }` | `html, body { background: #1a1a1a; color: #ddd; }` |
| `main { background: white; }` | `main { background: #222; }` |
| `header { border-bottom: 1px solid #e5e9ea; }` | `header { border-bottom: 1px solid #333; }` |
| `.chip { background: #f3f6f7; border: 1px solid #e5e9ea; }` | `.chip { background: #2a2a2a; border: 1px solid #3a3a3a; }` |
| `a { color: #006ed3; }` | `a { color: #4ea3ff; }` |

## Walkthrough: add a "Created" column to the image tiles

If you want to show the file's created time alongside the modified
date (the template currently shows `Date` which is the modified
date):

1. Note: `FileView.Date` is the only date field exposed. The
   `FileInfo` struct in the scanner *does* have `ModTime`
   (in nanoseconds) but no `CreatedTime`. Adding a "Created"
   column would require a code change — extend `FileView` with a
   `Created string` field, populate it in `RenderPage` from
   `info.ModTime` (or from `os.Stat` birthtime via
   `syscall.Statx` on Linux), then reference it in the
   template as `{{.Created}}`.

   In other words: this is a code change, not a template-only
   change. The template is fully driven by the Go struct fields
   — anything you want to display has to be in the struct.

## Where the bundled templates are defined in source

If you want to read the source (or fork and customise):

| File | Constant | Lines (approx) |
|---|---|---|
| `render.go` | `galleryTemplate` | line 392, ~574 lines (HTML + inlined CSS + inlined JS) |

A single constant. CSS and JS are inlined inside `<style>` and
`<script>` blocks respectively. To customise: edit the constant,
rebuild the module (`./build.sh`), restart Caddy (`sudo systemctl
restart caddy`). The on-disk template is written from the new
constant on the next startup.

For local-install (`./build.sh --user`, no sudo), there's no
systemd to restart — just kill the old `caddy run` process
(`kill $(cat ~/caddy.pid)`) and start the new one. The
bundled template is used automatically when no
`Caddyfile.user` `template` subdirective is set, so there's
nothing extra to do for the local case.

## Upgrading from a pre-inlining install (Phase 16 to Phase 17)

If your site was running the old 3-file template split
(`gallery.tmpl` + `style.css` + `lightbox.js`) and you upgraded
to the new inlining build, the on-disk files are in an
inconsistent state:

- `style.css` and `lightbox.js` from the old build are now
  dead weight. `writeBundledTemplates` removes them
  automatically on the next Provision after upgrade. Safe to
  ignore.
- The on-disk `gallery.tmpl` from the old build still has
  the old `{{template "lightbox.js" .}}` references, which
  no longer work. The new `loadTemplate` will fail to parse
  this file and the gallery will 500.

**One-time fix on upgrade:**

```
sudo rm /etc/caddy/gallery-templates/gallery.tmpl
sudo systemctl restart caddy
```

The next Provision writes the new inlined template. After that,
the on-disk file is the canonical inlined version, and any
operator edits to it become live overrides.

## Upgrading the bundled template content

`writeBundledTemplates()` deliberately does NOT overwrite an
existing `gallery.tmpl` on disk — that's the operator-override
contract. When the bundled template's contents change (e.g.
new CSS rule, new template branch, fixed layout bug), the
on-disk file is NOT updated automatically.

**To pick up the new bundled content:**

```bash
# 1. Save any local customisations (if you have any)
diff /etc/caddy/gallery-templates/gallery.tmpl      /home/osmanj/projects/caddy_media_gallery/render.go
# 2. Delete the on-disk file
sudo rm /etc/caddy/gallery-templates/gallery.tmpl
# 3. Restart Caddy so the next Provision runs writeBundledTemplates
sudo systemctl restart caddy
# 4. (Optional) Re-apply your local customisations
```

This workflow is intentional: operators who have customised the
template (e.g. themed dark mode) keep their changes across
upgrades. Operators who haven't customised just get the
"fresh bundled version" after the rm + restart.

**Future enhancement (not v1):** writeBundledTemplates could
write a content hash into a sidecar file, and on Provision, if
the sidecar hash doesn't match the bundled hash, overwrite the
on-disk file. This would auto-update without breaking the
operator-override contract (the operator could still delete the
on-disk file to fall back to the bundled version, OR write a
sidecar with a custom hash to pin to their version).

## Troubleshooting

**Edit took effect but the page looks the same.** Hard-reload
(Cmd-Shift-R / Ctrl-F5). The browser may have cached the
previous HTML.

**Edit took effect but the page is a 500.** Your template has
a parse error. The response body will contain the Go
`html/template` parser error. Common causes:
- Unclosed `{{if}}` / `{{range}}` / `{{with}}` blocks
- `{{end}}` mismatch (most common — Go templates require `{{end}}` to close every block)
- Calling a method that doesn't exist on the data type (e.g. `{{.Foo.Bar}}` when `Foo` is a string)
- An unescaped backtick inside a Go template comparison (backticks terminate the Go raw string the constant is in)

**Edit took effect but layout is broken.** You removed or
re-ordered a structural element. The CSS selectors and the JS
`querySelector` calls expect a specific DOM shape — search
for the class name in the template to see what's expected.

**Want to test a new template before deploying it.** Stage the
edit in a new file at, say, `/etc/caddy/gallery-templates/gallery.tmpl.staging`,
then copy it over the live file once you're happy. Or
`curl` the gallery to see the live HTML and check it with a
browser inspector before saving.

**Got a 500 immediately after upgrading.** You have the
pre-inlining on-disk `gallery.tmpl` from a previous build.
See the "Upgrading from a pre-inlining install" section above.

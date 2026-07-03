# Templates: dark mode and theme toggle

## Dark mode + theme toggle

The template supports three themes:

1. **Auto** (default) — follows the visitor's OS preference via the
   `prefers-color-scheme` CSS media query. macOS, Windows, iOS,
   and Android all expose this preference; if the visitor's OS is
   in dark mode, the gallery renders in dark mode automatically.
2. **Light** — forces light mode regardless of OS preference.
3. **Dark** — forces dark mode regardless of OS preference.

The choice is presented as a small toggle button group in the
page header. Three buttons: a gear icon (Auto), a sun icon (Light),
a moon icon (Dark). The current state is marked
with `aria-pressed="true"` and a slightly inset background.

**Position (Phase 50/51):** the toggle was originally at the
top-LEFT of the header (just to the left of the page title). As
of Phase 50, it sits at the top-RIGHT, where the old sort-order
indicator used to be. The sort indicator itself was removed in
Phase 50 (the sort UI still exists as the sort-bar below the
header — the active sort field is still visible there).

**Mobile layout (Phase 50/51):** on screens ≤600px wide, the
header stacks vertically via `@media (max-width: 600px) {
.header-top { flex-direction: column; }`. The visual order on
mobile is:

1. **Theme toggle** (top, right-aligned via `align-self: flex-end`)
2. **h1 + meta line** (below, full width)

This is achieved with CSS `order` on the flex children, not
by reordering the HTML source (the source order stays h1 →
meta → toggle, which is better for screen readers):

```css
.header-main { order: 2; }
.theme-toggle { order: 1; align-self: flex-end; }
```

The toggle's row order (`order: 1`) puts it above the header
content (`order: 2`) on mobile; the `align-self: flex-end` keeps
it right-aligned. On desktop (the default), neither `order` nor
`flex-direction` applies, so the layout reverts to the normal
horizontal flex with the toggle at the right (via the
`justify-content: space-between` on `.header-top`).

The choice persists across visits via `localStorage` under the key
`gallery-theme`. Values stored: `auto` | `light` | `dark`. `auto`
is the default (no attribute set on `<html>`); `light` and `dark`
set the `data-theme` attribute on `<html>`.

### No flash of wrong theme

There's a tiny inline `<script>` in the `<head>` that reads
`localStorage` and applies the `data-theme` attribute to `<html>`
BEFORE the body paints. This runs synchronously during HTML parsing,
so the CSS already sees the right theme when the body renders. No
flash of light theme when the visitor has chosen dark.

### How dark mode is implemented

All colors are defined as CSS custom properties (CSS variables) in
the `:root` selector at the top of the template's `<style>` block.
There are ~16 tokens, divided into semantic groups:

| Group | Tokens |
|---|---|
| Backgrounds | `--bg` (page), `--bg-card` (cards), `--bg-chip` (chips), `--bg-hover` (chip hover), `--bg-active` (active chip) |
| Text | `--fg` (primary), `--fg-muted` (secondary), `--fg-faint` (tertiary), `--fg-disabled` (disabled) |
| Borders & shadows | `--border`, `--border-strong`, `--shadow`, `--shadow-strong` |
| Accents | `--accent` (links + borders), `--accent-hover`, `--accent-bg` (button fills — separate from `--accent` so the dark-mode button can be a muted darker blue without dimming link text) |

The dark mode override is just a second block of token assignments
that applies when:

- `@media (prefers-color-scheme: dark) { :root:not([data-theme="light"]) { ... } }`
  — the OS is in dark mode AND the user hasn't explicitly picked
  light. The `:not([data-theme="light"])` selector is what makes
  the "Auto" state work: when the OS is dark but the user picked
  Light, the dark tokens don't apply.
- `[data-theme="dark"] { ... }` — manual dark mode regardless of
  OS preference. Triggered by clicking the moon icon; choice
  persists in localStorage.

**Why `--accent` and `--accent-bg` are separate tokens:** the
accent color serves two visual roles — text/borders (good as a
bright color on a dark bg, e.g. `#4dabff`) and button fills
(looks glaring as a bright color, should be muted, e.g. `#3b6fb6`).
Splitting them lets each role have its own dark-mode value.

### Dark mode refinements (Phase 40–42)

The first dark-mode pass had two issues:

- **Chip bg too light** (Phase 40): the chips (directories, other
  files, sort buttons, page buttons) had a `--bg-chip` value
  (`#1d1d1d` in dark mode) that was visibly lighter than the page
  bg (`#1a1a1a`), making the chips stand out as bright blobs. Fixed
  by setting `--bg-chip: #1a1a1a` (same as `--bg`) in dark mode, so
  chips blend in with the page; only the border + text show. This
  mirrors the light-mode behavior (where `--bg-chip` already equals
  `--bg`).
- **Hardcoded colors in 11 CSS rules** (Phase 41): a regex-based
  refactor (the original dark-mode implementation) caught 18 rules
  but missed 11 more that used hardcoded light-mode hex values.
  After a comprehensive sweep, all CSS rules now use `var()` token
  references. The only `#hex` colors left in the on-disk template
  are: the token definitions themselves (light + dark overrides),
  the video tile placeholder gradient (theme-independent), and the
  play button colors (white-on-dark, theme-independent).

### Customizing colors

To override a color, edit the on-disk template at
`/etc/caddy/gallery-templates/gallery.tmpl` and change the token
value in the `:root` block. For example, to make the accent color
green instead of blue:

```css
:root {
  --accent: #2e8b57;       /* was #006ed3 */
  --accent-hover: #3aa86a; /* was #0095e4 */
  --accent-bg: #2e8b57;    /* was #006ed3 — used by active sort/page buttons */
}
```

To add a new dark-mode-specific color, define it in all three
blocks (`:root`, the media query, and `[data-theme="dark"]`).

### Lightbox (always dark)

The lightbox overlay (the full-screen image/video viewer) has its
own dark colors that are theme-independent — the dark background
and white controls work in both modes. This is intentional: a dark
overlay focuses attention on the content regardless of the page
theme.

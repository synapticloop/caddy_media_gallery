# Templates: header, pagination, footer, and layout features

## Header meta line format

The page header (below the page title) shows a meta line summarizing
the directory's contents. The format (Phase 44/45/55) is:

```
34 images · 8 videos · 2 other files // (8.3 MB total) // 4 directories · 50 per page · Page 1 of 2
└─────┬─────┘ └────┬────┘ └─────┬───────┘ └──────┬───────┘  └───┬─────┘ └──┬─────┘ └──┬───────┘
      count       count       count            total size     count     page size page indicator
                                              (ALL files:                              (when
                                              images + videos                          TotalPages
                                              + other files)                           > 1)
```

Note the format after the `//` size segment: the directory count
uses a single **space** separator (not `·`), then the rest of the
items use `·`. This was changed in Phase 55 — the size is
visually marked as "special" by the `//` framing, and the regular
meta items after it use a single character (`·` or space)
consistently.

**Meta items, in order:**

1. **Image count** — `N images`. Always shown.
2. **Video count** — `· N videos`. Shown only when `N > 0`.
3. **Other files count** — `· N other files`. Shown only when `N > 0`.
4. **Total size** — `// (X.X KB total) //`. Wrapped in `//`
   separators (visually distinct from the `·` separator used for
   the file counts). Represents the SUM of `Size` for all files in
   the directory: images + videos + other files. Excludes
   subdirectories. The literal word `total` follows the size
   inside the parens, making the meaning clear. Pre-formatted via
   the `humanSize()` helper (B / KB / MB / GB). After the closing
   `//`, the next meta item is separated by a single space
   (not `·`).
5. **Directory count** — `N directories` (single space separator
   after `//`, then the count). Shown when the user is in a
   subdir (N = subdirs of the current dir) or when there are
   subdirs in the root listing.
6. **Per page size** — `· 50 per page`. Always shown.
7. **Page indicator** — `· Page X of Y`. Shown only when
   `TotalPages > 1` (multi-page gallery).

**Why the size uses `//` separators instead of `·`:** the `//`
visually distinguishes the size from the other meta items (which
all use `·`). The size is conceptually different — it's a
quantity (bytes), not a count. The `//` is a "this is special"
marker.

**Implementation:** the meta line is rendered by the template at
the top of the `<style>` block's wrapping `<header>`. The size
segment uses three separate `<span>` elements (one for each `//`,
one for the parens) so the browser's flex `gap: 0.5rem` adds
visual spacing between them.

## Open-in-new-tab button

The template has a small **↗** button on each image/video tile
(top-right corner of the card) that opens the file's URL in a
new browser tab. Added in Phase 47; refined in Phase 52.

**Visual:**
- Position: `top: 6px, right: 6px` inside the `.card`
- Size: 28×28 px
- Background: `rgba(255, 255, 255, 0.85)` — light translucent
- Dark border: `border: 2px solid #000` (Phase 47) so the button
  stands out more
- Arrow color: `#111111` (Phase 52) — a fixed dark color,
  **NOT** the theme-aware `--fg` token
- The character itself is `north east arrow` (U+2197 NORTH EAST ARROW, displayed as the arrow glyph in the bundled template)
- Default opacity: `0.5` (subtle). On hover/focus, opacity goes
  to `1.0` and the button scales up slightly.

**Why the arrow color is fixed `#111111` (not `var(--fg)`):**
the button has a light translucent background
(`rgba(255,255,255,0.85)`) that stays light over ANY page
background (light or dark). A dark arrow on a light button
background is always visible — no need to adapt to the page's
theme. The `--fg` token stays at its current values
(`#111111` light / `#e5e5e5` dark) and continues to be used by
other elements (h1, body, meta, sort buttons, etc.) — only
`.open-btn` is excluded from the theme-aware color rule.

**Behavior:**
- Click or Enter on the button → `window.open(href, '_blank',
  'noopener,noreferrer')` opens the file in a new tab
- The `noopener,noreferrer` flags are a security best practice
  (prevents the new tab from accessing `window.opener`)
- The click event is `stopPropagation`'d so it doesn't also
  trigger the parent's "open in lightbox" handler

## Pagination

The pagination nav is rendered in **two** places (Phase 54):

1. **Top** — after the sort-bar (Name/Type/Modified/Size buttons), before the DIRECTORIES section
2. **Bottom** — after the IMAGES grid (existing position)

Both pagination navs are identical — same buttons (`Prev`,
page numbers, `Next`), same styling, same conditional (only
shown when `TotalPages > 1`). Only the position differs.

**Why mirror:**

- Visitors on a long page don't have to scroll back to the top
  to switch pages — they can click `Next` at the top.
- The pagination at the top also serves as a "you are here"
  indicator when arriving on a non-first page (so the user knows
  they're not on page 1).
- Symmetry: the sort-bar is at the top, the pagination at the
  bottom; having pagination at the top mirrors the bottom.

The same `{{if gt .TotalPages 1}}` conditional guards both
instances, so single-page galleries don't show any pagination
at all.

**Pagination item format:** uses the same Google-style pattern
introduced in Phase 29 — `First` (when far from start),
`Prev`, page numbers with ellipsis in the middle, `Next`,
`Last` (when far from end). The `aria-pressed` is set on the
current page number for accessibility.

## Footer

At the bottom of every gallery page, below the IMAGES grid (and
below the bottom pagination if multi-page), a small footer credits
the underlying technologies (Phases 56/58/59):

```
─────────────────────────────────────────
           proudly served by caddy + synapticloop // media gallery
```

- **"caddy"** — links to https://caddyserver.com (the web server)
- **"synapticloop // media gallery"** — links to
  https://github.com/synapticloop/caddy_media_gallery (this
  plugin's repo)

Both links have `rel="noopener" target="_blank"` (security best
practice for new-tab links).

**Styling:** centered, small text (`0.8rem`), muted color
(`var(--fg-muted)`), subtle padding. No border-top (removed in
Phase 58 — the muted color provides enough visual distinction).

**Why these credits:**
- "caddy" — Caddy is the web server. Without it, this plugin
  wouldn't exist; credit the underlying tech.
- "synapticloop // media gallery" — links to this plugin's repo
  (not just the synapticloop org page) so visitors can read the
  source, file issues, or fork the project.

The footer text is operator-visible but not configurable — if
you want to change it, edit the template (`<footer class="site-footer">`
in the bundled template). Operators who fork the project can
brand the footer however they like.

## Section heading: "MEDIA" (not "IMAGES")

The gallery renders both **images** AND **videos** (since Phase 25
when the lightbox got video support). To reflect the broader scope,
the IMAGES section heading was renamed to **MEDIA** in Phase 60.

In the bundled template:

```html
<h2 class="section-heading">Media</h2>
```

If you fork the template and want to revert to "IMAGES" (e.g., you
have a strictly-photo gallery with no videos), change this line.
The CSS (`.section-heading`) is unchanged — only the visible text.

The other section headings remain "Directories" and "Other files"
(unaffected by Phase 60).

# Templates: localStorage keys

## What the template stores in localStorage

Per user request 2026-07-01: this section is the canonical
reference for every key the template writes to `localStorage`.
Use this when debugging "why is the visitor seeing X?" or when
adding new persisted state to the template.

All keys are namespaced with the `gallery-` prefix so they
don't collide with the host site's localStorage usage (the
module is mounted under `/images/*`, but the visitor may have
already visited other parts of the host site with their own
`localStorage` keys).

| Key | Values | Source | Read on | Written on |
|---|---|---|---|---|
| `gallery-theme` | `"light"` \| `"dark"` | Theme toggle in header | Inline script in `<head>` (before body paint, so no theme flash) — see [Dark mode + theme toggle](03c-feature-dark-mode.md) | Click on theme toggle button |
| `gallery-dirs-sort` | `"name"` (default) \| `"modified"` (server-side default — operator-configurable) | Directories table header click | Dirs-table sort script — see [Header meta line format](03f-feature-layout.md) (the sort bar lives in the header) | Click on a dirs-table column header |
| `gallery-dirs-order` | `"asc"` \| `"desc"` | Directories table header click | Same | Same |
| `gallery-others-sort` | `"name"` \| `"modified"` (default) | Other-files table header click | Same | Same |
| `gallery-others-order` | `"asc"` \| `"desc"` | Other-files table header click | Same | Same |
| `gallery-section-<section>` | `"collapsed"` | The [−]/[+] toggle on the directories / other-files section headings | Section-toggle script (eager: on page load, the script reads all `gallery-section-*` keys and applies the collapsed class) | Click on a section heading (or the small toggle button) |
| `gallery-lb-exif-collapsed` | `"true"` (absent = expanded) | Click on the EXIF panel header in the lightbox | Lightbox JS — when opening, if the key is `"true"` the panel starts collapsed | Click on the EXIF panel header to collapse (key removed on expand) |
| `gallery-lb-video-meta-collapsed` | `"true"` (absent = expanded) | Click on the META panel header in the lightbox (videos only) | Lightbox JS — same pattern as EXIF | Same |

`<section>` is one of `dirs` or `others` — same values used in
the `data-section="..."` attribute on each section header.

### Why localStorage (not URL query) for these states?

- **Theme**: personal preference, not bookmarkable intent. Would
  pollute shared links.
- **Table sort state**: same reasoning — the visitor wants to
  remember *their* preferred sort, but a shared link to the
  gallery shouldn't force the recipient into a particular sort.
  The URL still carries the sort (so a NEW visitor with no
  localStorage gets the URL's sort), but the visitor's own
  choice persists across reloads in localStorage.
- **Section toggle**: pure UI preference; bookmarkable intent
  would be confusing ("why doesn't this link show the directories?").

For all three: localStorage = persistent + per-visitor + no
URL noise. URL is the "first-time canonical" path; localStorage
is the "I've been here before" path.

### Why the `gallery-` prefix matters

If you fork this template, **change the prefix** to your own
namespace (e.g. `myphotos-`). Otherwise, two galleries hosted
on the same hostname would share theme/sort/toggle state in
unexpected ways (the visitor picks "dark" on site A, and their
next visit to site B is also dark).

This is also why the template NEVER reads localStorage keys
without the prefix — a `localStorage.getItem('theme')` would
be a security/UX bug.

### Dirs / Others table sorting

Each of the two tables (directories and other files) has its own
sort state. The state resolution is:

1. URL query (`?dirs_sort=name&dirs_order=asc`) — the canonical
   state. A shared link forces the sort on first visit.
2. localStorage (`gallery-dirs-sort` + `gallery-dirs-order`) —
   the visitor's remembered preference.
3. Server-side default — operator-configurable in the template
   (the JS reads `data-default-sort` / `data-default-order`
   attributes on the table headers).

On click:
- Same column → toggle direction (asc → desc, desc → asc)
- Different column → use that column's `data-default-order`
- Sort client-side (no server round trip — read rows once,
  append in new order)
- Save to localStorage
- Update URL via `history.replaceState` so the back button
  still works (no history pollution, just a silent URL
  replacement)

The sort fires immediately and updates the table within a
single frame. No page reload, no spinner.

### Section toggle (the [−] / [+] button)

The directories table and the other-files table each have a
collapse-toggle button. Per Phase 71/74: clicking the WHOLE
section heading (the `<h2>` row) toggles the state — the small
button is still rendered for keyboard/screen-reader users.

The state is stored under `gallery-section-<section>` where
`<section>` is the value of the `data-section` attribute on the
`<h2>` (e.g. `dirs`, `others`). Value `"collapsed"` means
collapsed; absence of the key means expanded (default).

Why localStorage for this and not URL state?
A URL query would mean: shared links always force-collapse
the dirs section. That's annoying — the visitor can't share
a link that's "the dirs section expanded". localStorage is
"remember MY last choice" with no URL pollution.

### Clearing the template's localStorage

To reset the visitor's preferences (useful during operator
testing):

```js
[
  "gallery-theme",
  "gallery-dirs-sort", "gallery-dirs-order",
  "gallery-others-sort", "gallery-others-order",
  "gallery-section-dirs", "gallery-section-others",
].forEach(k => localStorage.removeItem(k));
```

Or via DevTools: `Application > Storage > Clear site data`.

The next page load after clearing will use all server-side
defaults — light theme (or auto-follow), name-asc dirs sort,
date-desc others sort, both sections expanded.

### localStorage unavailable?

Every read/write site has a `try { ... } catch (e) {}` wrapper.
If localStorage is disabled (private/incognito mode in some
browsers, disabled cookies/site-data, quota errors), the
template silently falls back to the server-side defaults —
no JS errors, no broken page. Visitors on shared/embedded
browsers (kiosks, smart TVs) just get the defaults, which is
the safe choice.

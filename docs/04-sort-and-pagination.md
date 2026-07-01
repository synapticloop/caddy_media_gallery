# Sort & Pagination

The gallery's image grid respects a small set of URL query
parameters. All are optional. They're a stable URL API —
bookmarking `?sort=size&order=desc&page=3` works and gives the
same view on every visit.

## Query parameters

| Param | Values | Default | What it does |
|---|---|---|---|
| `?sort=` | `name` / `type` / `size` / `mtime` | `mtime` | What field to sort images by. The URL also accepts `?sort=date` (treated as `?sort=mtime`) for back-compat. See "Aliases" below. |
| `?order=` | `asc` / `desc` | `desc` | Sort direction. `desc` for `mtime` is newest-first; `desc` for `name`/`type`/`size` is Z-A / largest-first. |
| `?page=` | integer &gt;= 1 | `1` | Which page of results to show. 1-based. Out-of-range or non-numeric values fall back to 1. |
| `?page_size=` | any value in the operator-configured `page_sizes` list | first item | Per-page size (driven by the dropdown). Changing this **resets the visitor to page 1** (the current `?page=` is dropped from the form's hidden inputs). Unknown values fall back to the first item. |
| `?q=` | free text (URL-encoded) | (none) | Server-side filename search. The match rule (`word` or `substring`) is operator-configured via `search_match`. |
| `?type=` | comma-separated extensions (e.g. `jpg,png`) | (none) | Server-side type filter. The form-submission version uses repeated `?ext=jpg&ext=png` (both work). |

The URL parameters are preserved across changes: clicking
"Name" on `?sort=mtime&order=desc&page=2` becomes
`?sort=name&order=asc&page=2` (the toggle flips the order for
the new field; the page stays the same). Same for the type
filter, the search box, and the page-size dropdown — all
preserve the other params via hidden inputs in their forms.

The **directory strip and other-files strip have their own
sort behaviour**:

- Directories: always rendered, case-insensitive alphabetical.
  The Directories table has its own click-to-sort headers
  (name, # items, # sub-dirs, size, modified). Sort state
  persists in `localStorage` (per table) and in the URL
  (`?dirs_sort=...&dirs_order=...`). See
  [docs/03-templates.md#what-the-template-stores-in-localstorage](../docs/03-templates.md#what-the-template-stores-in-localstorage)
  for the full key reference.
- Other files: scanner order (newest-first by mtime). The
  Other Files table has its own click-to-sort headers
  (name, type, size, modified). Same persistence as Directories (see the
[localStorage reference](../docs/03-templates.md#what-the-template-stores-in-localstorage)).

The main image sort does NOT affect the dirs/other-files
sorts. This is intentional: the dirs strip is a navigation
aid and should be stable, not reshuffle when the user changes
the image sort.

## Sort indicator in the header

The header shows the current sort + a reset link:

- On the **default sort** (`mtime desc`), the indicator is a
  plain `<span>` — clicking it would just reload the same
  page, so it's not a link. It reads `Sort: Modified $\downarrow$`.
- On **any other sort**, the indicator is a link to `?` (no
  query params, which is the default). It reads e.g. `Sort: Size $\uparrow$`.

## Sort buttons

The sort bar at the top of the header has four buttons: Name,
Type, Modified, Size. Each shows a `$\uparrow$` or `$\downarrow$` arrow if it's
the currently active sort. Clicking the active button toggles
the order; clicking an inactive button switches to that field at
the direction that's the "natural" opposite of the current
order (so you don't get the same direction twice in a row).

The button labels are produced by the `sortLabel` template
function (see the [Templates](03-templates.md) doc for the full
map). To add a new sort field:

1. Add a `case` to `sortLabel` in `render.go`
2. Add a `case` to the `sortFiles` function (the sort
   implementation)
3. Add a button to the `gallery.tmpl` template

It's a code change in three places; the template is just the
visible button.

## Pagination

The per-page size is configured via the `page_sizes` Caddyfile
directive (default: `60 30 120 all`). The FIRST item in the
list is the default per-page count (used when `?page_size=`
isn't in the URL). The dropdown is rendered in the meta line
of the gallery HTML so the visitor can switch the per-page
size live.

**Page-size change resets to page 1.** When the visitor changes
the per-page size via the dropdown, the form omits the `?page=`
hidden input. The next page load is page 1 — the visitor
doesn't end up on a non-existent page (e.g. changing from
60-per-page to 120-per-page on page 2 of a 100-image dir
would otherwise land on an empty page 2).

The "Showing 1-60" range in the media header shows the
visitor the slice of items they're seeing — e.g. for a
200-image gallery, page 2 shows "Media (200 - Showing 61-120)".

Pagination links in the live template:
- The "First" and "Prev" pagination buttons (rendered by the template with double- and single-left-arrow icons, when `{{.HasPrev}}` is true)
- The "Next" and "Last" pagination buttons (single- and double-right-arrow icons, rendered when `{{.HasNext}}` is true)
- Current page indicator in the middle

The page numbers in the URL are 1-based, not 0-based. `?page=0`
falls back to `?page=1`.

## Search

The search box in the header does two things:

- **As the visitor types** (client-side filter): items that
  don't match get the `.filtered-out` class. Visibility
  collapse + opacity 0 fade hides them. The URL doesn't
  change.
- **"Search all" button** (server-side filter): submits the
  form with `?q=foo`. The page re-loads with the matched
  files only. The form preserves the other URL params.

The match rule is the same on both sides: `word` (default
"substring" without config) or `word` (when the operator sets
`search_match word`). The visitor doesn't need to know — the
match just works.

## Type filter

The "Type Filter" dropdown has checkboxes for Images / Videos /
Other, with the count of each type next to the label. Checking
"Images" + "Videos" (unchecking "Other") filters out non-media
files. The "Reset" pill resets the filter.

**The `(none)` entry** — the Other dropdown includes a
special `(none)` option for files that don't have an
extension (e.g. `Makefile`, `welcome`). When checked,
ONLY files without an extension are shown — no `.md`,
no `.txt`, no other recognized extension. The form value
is the sentinel `.` (a literal dot) which can't be a real
file extension; `parseTypeFilter` translates it to the
empty-string filter key, and `applyTypeFilter` checks
`filter[""]` to apply the strict filter. Multi-select
works as expected: checking `(none)` + `.md` shows files
matching either condition.

The filter is applied both server-side (when the form is
submitted) and via the URL (`?type=jpg&type=png`). Directories
are never filtered (the user can always navigate).

## Examples

```
/images/                                 # default: mtime desc, page 1
/images/?sort=name&order=asc            # alphabetical
/images/?sort=size&order=desc           # largest first
/images/?page=3                          # third page of default sort
/images/?sort=type&order=asc&page=2
/images/?page_size=120                   # change per-page to 120
/images/?q=cat                           # server-side search for "cat"
/images/?type=jpg,png                   # only JPG and PNG
```

## Aliases

`?sort=date` is an alias for `?sort=mtime` — both sort by
the file's modification time. The two values produce
identical results:

```
/images/?sort=date&order=desc    # == /images/?sort=mtime&order=desc
/images/?sort=date&order=asc     # == /images/?sort=mtime&order=asc
```

**Why have the alias?** The two names describe the same
thing from different angles — "mtime" is the technical
field name (modification time, from the filesystem), "date"
is the user-friendly name (the date the file was last
changed). The "Modified" sort button in the UI emits
`?sort=mtime` (the technical name), but the URL API accepts
both for back-compat with bookmarks and external links that
might use either name.

**Implementation:** `parseSort` in `render.go` accepts
`"date"` in its switch statement and treats it identically
to `"mtime"`:

```go
switch field {
case "name", "type", "date", "mtime", "size":
    // all valid; "date" is an alias for "mtime"
default:
    field = "mtime" // unknown fields fall back to the default
}
```

In `sortFiles`, both `"mtime"`, `"date"`, and `""` (the empty
default) share the same sort branch — sort by `ModTime` per
the `order` param. So functionally there's no distinction
between them.

**When to use which:** if you're writing a new integration
or bookmark, prefer `?sort=mtime` (it's the canonical name
and what the UI button emits). `?sort=date` is fine too —
just pick one and stick with it for consistency.

## Stability guarantees

- The URL parameter API is **stable**. Bookmarking works. The
  four sort fields, two orders, the search query, the type
  filter, and the per-page size are part of the public
  contract; renaming or removing one is a breaking change.
- Adding a new sort field is non-breaking (just adds a new
  option).
- The default sort may change in a future major version, but
  the `?sort=mtime` URL will always be honored.
- Page size may change between versions, but URLs with
  `?page=N` are stable relative to the *current* page size —
  if the page size changes from 60 to 100, page 3 of the old
  URL might now be page 2 of the new layout, but no image is
  lost or duplicated (it's the same image set, just packed
  differently).
- Search match mode (`?q=...`) is stable; the operator can
  change the match rule (word/substring) at any time and old
  bookmarks still work the same way.

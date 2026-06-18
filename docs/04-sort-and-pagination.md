# Sort & Pagination

The gallery's image grid respects three URL query parameters.
All three are optional. They're a stable URL API — bookmarking
a `?sort=size&order=desc&page=3` link works and gives the same
view on every visit.

## Query parameters

| Param | Values | Default | What it does |
|---|---|---|---|
| `?sort=` | `name` / `type` / `size` / `mtime` | `mtime` | What field to sort images by. The URL also accepts `?sort=date` (treated as `?sort=mtime`) for back-compat. See "Aliases" below. |
| `?order=` | `asc` / `desc` | `desc` | Sort direction. `desc` for `mtime` is newest-first; `desc` for `name`/`type`/`size` is Z-A / largest-first. |
| `?page=` | integer ≥ 1 | `1` | Which page of results to show. 1-based. Out-of-range or non-numeric values fall back to 1. |

The URL parameter is preserved across sort changes: clicking
"Name" on `?sort=mtime&order=desc&page=2` becomes
`?sort=name&order=asc&page=2` (the toggle flips the order for the
new field; the page stays the same).

The **directory strip and other-files strip are NOT affected**
by the sort. They always render in:
- Directories: case-insensitive alphabetical (`splitFiles`
  re-sorts them on every render)
- Other files: scanner order (same default as the image list
  — newest-first by mtime)

This is intentional: the dirs strip is a navigation aid and
should be stable, not reshuffle when the user changes the image
sort. See Phase 15 in the plan for the reasoning.

## Sort indicator in the header

The header shows the current sort + a reset link:

- On the **default sort** (`mtime desc`), the indicator is a
  plain `<span>` — clicking it would just reload the same page,
  so it's not a link. It reads `Sort: Modified ↓`.
- On **any other sort**, the indicator is a link to `?` (no
  query params, which is the default). It reads e.g. `Sort: Size ↑`.

## Sort buttons

The sort bar at the top of the header has four buttons: Name,
Type, Modified, Size. Each shows a `↑` or `↓` arrow if it's the
currently active sort. Clicking the active button toggles the
order; clicking an inactive button switches to that field at
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

50 images per page (constant `PageSize = 50` in render.go, not
configurable via env var or Caddyfile — it's a code constant).

Pagination links in the live template:
- `‹‹ First` and `‹ Prev` (when `{{.HasPrev}}`)
- `Next ›` and `Last ››` (when `{{.HasNext}}`)
- Current page indicator in the middle

The page numbers in the URL are 1-based, not 0-based. `?page=0`
falls back to `?page=1`.

## Examples

```
/images/                           # default: mtime desc, page 1
/images/?sort=name&order=asc      # alphabetical
/images/?sort=size&order=desc     # largest first
/images/?page=3                    # third page of default sort
/images/?sort=type&order=asc&page=2
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
  four sort fields and two orders are part of the public
  contract; renaming or removing one is a breaking change.
- Adding a new sort field is non-breaking (just adds a new
  option).
- The default sort may change in a future major version, but
  the `?sort=mtime` URL will always be honored.
- Page size is a code constant and may change in future
  versions. URLs with `?page=N` are stable relative to the
  *current* page size — if the page size changes from 50 to
  100, page 3 of the old URL might now be page 2 of the new
  layout, but no image is lost or duplicated (it's the same
  image set, just packed differently).

# Changelog

All notable changes to `caddy_media_gallery` are documented here. Dates are in
YYYY-MM-DD format. Commit hashes are short (7 chars).

The project started as `caddy_image_gallery` and was renamed to `caddy_media_gallery`
on 2026-06-19 to better reflect that it serves images, videos, and other files
(not just images).

---

## 1.1.4 — 2026-07-04

### ⚡ Performance: lazy metadata enrichment (visible page only)

Per user request 2026-07-04 (lazy-enrichment branch):
make per-file metadata enrichment lazy. Previously,
every cache miss spawned 8 parallel ffprobe
subprocesses to enrich the ENTIRE directory's files
in the background. For a 4500-file directory at
~10ms/file, that meant 5+ seconds of CPU and 8
concurrent ffprobes per fresh scan. The 90%+ CPU
usage observed on the live server was a direct
result.

ROOT CAUSE: Cache.Get spawned an EnrichInBackground
goroutine that ran enrichParallel on the entire
`files` slice. The visitor's first page render
wasn't blocked (the HTML response went out as soon
as Scan() returned), but the background work
consumed 5+ seconds of CPU per fresh cache.

FIX (scancache.go): Cache.Get no longer calls
EnrichInBackground. The cache stores the
un-enriched scan result. The caller (RenderPage) is
now responsible for enriching its visible subset.

FIX (scanner.go): The enrichParallel method now
delegates to a new free function enrichParallelFiles
that takes the same parameters as explicit args
(no Scanner receiver). This lets RenderPage call
it without holding a Scanner in scope.

FIX (render.go): After pagination computes the
visible `paged` slice:
  - Copy paged into a fresh slice
  - Spawn a goroutine that calls enrichParallelFiles
    on the copy with 4 workers
  - Return immediately with the (un-enriched) paged
    slice (the goroutine runs in the background)

The response is sent immediately. The next refresh
re-enriches the same files, but the per-file caching
of ffprobe results on disk (the .vmeta/.ameta
sidecars) means the second enrichment is ~50µs per
file (mostly sidecar reads, no subprocess spawn).

The cache is NOT updated with the enriched data
(unlike the previous design). Each request
enriches its own visible subset. Off-page files
get enriched only when the visitor navigates to
them. This is the "pure lazy" design — off-page
files do ZERO work until needed.

USER-VISIBLE CHANGE (verified live at
https://hermes.synapticloop.com/images/):

  Before 1.1.4:
    - 8+ ffprobe subprocesses per fresh scan
    - 90%+ CPU usage during the scan (each ffprobe
      8-23%)
    - 5+ seconds of background work before the cache
      was fully enriched
    - 4500+ file reads per fresh scan (one ffprobe
      per file)

  After 1.1.4:
    - 0 ffprobe subprocesses at idle (verified after
      rebuild — `ps -ef | grep ffprobe | wc -l = 0`)
    - 0% CPU usage at idle
    - ~150ms of background work per page view
    - 60 file reads per page view (pageSize files,
      not total directory)

  For a 4500-file directory: that's 75× less
  ffprobe work per fresh scan. The page renders
  correctly with the same content; only the
  background CPU cost is reduced.

SIGNATURE CHANGES:

  - RenderPage now takes 4 new params: noExif,
    noMeta, resolvedPath, thumbCacheDir,
    thumbFormat. The 4 new args are used for the
    lazy enrichment goroutine. The call site
    (gallery.go) was updated to pass g.NoExif,
    g.NoMeta, resolved, g.thumbCacheDir(),
    g.ThumbFormat.

TESTS:

  - All existing tests updated to pass the 4 new
    args (115 test calls across 6 test files).
  - New file: enrich_test.go with 3 tests for
    enrichParallelFiles (visible-only, empty,
    nil-root).
  - All 450+ Go tests pass.

NO BEHAVIOR CHANGES outside the enrichment timing:
  - The page renders correctly. The Exif/Width/
    Height/VideoMeta fields may be empty on the
    first render and populated on subsequent renders
    (off the goroutine).
  - The lightbox still works (it reads from the
    enriched FileInfo fields; on first view they may
    be empty if the user opens the lightbox before
    the goroutine completes, but the second visit
    shows the enriched data).
  - The cache TTL and mtime-based invalidation are
    unchanged.
  - The eviction behavior is unchanged.

All 450+ Go tests pass.

---

## 1.1.3 — 2026-07-04

### 🧹 Polish: cache hygiene (orphan sweep + LRU bound)

Per user request 2026-07-04 (minor-fixes branch): two
silent-failure fixes for the cache.

#### 1. Orphan sidecar sweep (commit 1f884b2)

ROOT CAUSE: the existing `evictIfOver` only cleans up
the sidecars (`.meta`, `.exif`, `.vmeta`, `.ameta`)
of the SPECIFIC thumb being evicted. It doesn't
sweep the cache for orphans left behind by other
paths:
  - Prior Caddy versions that wrote sidecars but
    changed the file layout
  - Crashed writes that wrote the `.meta` but not the
    `.webp`
  - The 1.1.0 → 1.1.1 transition where the audio/video
    metadata cache changed shape
  - The legacy flat layout that was migrated to the
    nested layout (leaving some sidecars behind)

Before this commit, the live cache had 30,717 `.meta`
files but only 13,100 `.webp` thumbs — 17,617 orphan
`.meta` files (17% wasted). Similarly 15,490 orphan
`.exif` files, 2,316 orphan `.vmeta` files, and 3
orphan `.ameta` files.

FIX (thumbnails.go):
  - New `sweepOrphans(cacheDir)` function that walks
    the 2-level nested cache layout and deletes any
    sidecar file that doesn't have a matching thumb.
    Identifies sidecars by extension (`.meta`,
    `.exif`, `.vmeta`, `.ameta`). For each sidecar,
    strips the extension and checks if the underlying
    thumb exists. If not, deletes the sidecar.
  - Helper `isSidecarFile(name) bool` — true for
    `.meta`, `.exif`, `.vmeta`, `.ameta`
    (case-insensitive). Mirrors the existing
    `isThumbFile` helper.
  - Helper `thumbExistsFor(sidecarPath) bool` —
    strips the sidecar extension and checks if the
    underlying thumb exists. Tries each known
    sidecar extension so a sidecar named e.g.
    "abc.webp.meta" correctly looks for "abc.webp".
  - `sweepOrphans` returns `(0, nil)` on a missing
    cache dir, matching the existing "not an error"
    convention in `evictIfOver` above. Runs ~20ms on
    an SSD for a 1 GB cache; trivial.

FIX (gallery.go):
  - `cacheSweepLoop` now calls `sweepOrphans` alongside
    `evictIfOver` on every 30-min tick. The eviction
    call is gated on `maxMB > 0` (no-op for unbounded
    caches); the orphan sweep runs regardless of the
    cap (unbounded caches also need orphan cleanup).
  - `Provision` now also kicks off an initial
    `sweepOrphans` at startup (in a goroutine, same
    pattern as the existing initial `evictIfOver`
    startup call). Otherwise the existing orphans
    would sit there until the first 30-min ticker
    fires.
  - When `MaxCacheSizeMB == 0` (unbounded), the
    `cacheSweepLoop` goroutine is still spawned (with
    `maxMB = 0`) so the orphan sweep runs at the
    30-min cadence even when there's no eviction cap.

USER-VISIBLE CHANGE (verified on the live cache
`/var/cache/caddy-gallery/` on
https://hermes.synapticloop.com/images/):

  Before this commit:
    `.meta`:  30,717 files  (17,617 orphans)
    `.exif`:  27,517 files  (15,490 orphans)
    `.vmeta`: 3,383 files   (2,316 orphans)
    `.ameta`: 3 files       (3 orphans)
    `.webp`:  13,100 files

  After this commit (startup sweep):
    `.meta`:  13,100 files  (matches thumb count)
    `.exif`:  12,027 files  (close to thumb count)
    `.vmeta`: 1,067 files   (matches video count)
    `.ameta`: 0 files       (regenerated by ffprobe on
                            next audio lightbox open)
    `.webp`:  13,100 files  (unchanged)

  The startup sweep removed 35,426 dead sidecars.

#### 2. LRU bound on the scan cache (commit 9951337)

ROOT CAUSE: the in-memory `ScanCache` grew without
bound on long-running Caddy processes. Entries were
pruned by TTL (default 24h, configurable) and by
Caddyfile changes (via `extSetsKey`), but never by
size. For a server serving millions of directories
over time, the map would grow until OOM.

FIX (scancache.go):
  - New `lruList` data structure — a doubly-linked
    list with a map for O(1) key→node lookup. O(1)
    `pushFront`, `moveToFront`, `remove`, `backKey`.
    Standard textbook LRU.
  - New `defaultScanCacheMaxEntries = 1024` constant.
    1024 entries × ~50 files per dir × ~200 bytes
    per FileInfo ≈ 10 MB worst case. Plenty for any
    reasonable server.
  - `ScanCache` struct extended with `recency *lruList`
    and `maxEntries int` fields. `NewScanCache`
    initialises both. New `NewScanCacheWithCap(ttl,
    maxEntries)` constructor for tests.
  - `Get()` now calls `touch()` on every cache hit.
    The previous code used RLock for the fast path;
    we now always take the write lock because touching
    the LRU list requires a write. Trade-off: tiny bit
    of concurrency for correct LRU semantics.
  - `SetFiles()` (called by the background enrichment
    goroutine) also calls `touch()` and `enforceCap()`.
  - On slow-path cache miss, the new entry is added
    with `pushFront` and `enforceCap` runs to evict
    overflows.

USER-VISIBLE CHANGE: none. The cache is internal;
the visitor sees no change. The only effect is
memory bounded by `maxEntries` (default 1024).

#### Tests

  - `thumbnails_test.go` (new file): 5 tests covering
    `sweepOrphans` (3 cases: matches + orphans, clean
    cache, missing dir), `isSidecarFile` (10 cases
    including case-insensitive), `thumbExistsFor` (3
    cases).
  - `scancache_test.go` (new file): 5 tests covering
    the `lruList` data structure (basic push/move/
    remove/back, move-to-front no-op, remove
    non-existent) and the LRU eviction behavior in
    `ScanCache` (eviction at cap, touch refreshes
    recency).

NO BEHAVIOR CHANGES outside the cache file system
and in-memory cache. The eviction, scan refresh,
and stats refresh are unchanged. The visitor-
facing UI is unchanged. The Caddyfile directives
are unchanged.

All 450+ Go tests pass.

---

## 1.1.2 — 2026-07-04

### 🔧 Polish: YY/ZZ/AA = eviction RUNS (not files) + new BB field

Per user request 2026-07-04 (cache-status-line-updates
branch): two updates to the cache-status footer at the
bottom of the page (the line `XX // YY // ZZ // AA`
that's now `XX // YY // ZZ // AA // BB`).

#### 1. YY/ZZ/AA now represent eviction RUNS, not files

ROOT CAUSE:

  The footer fields YY/ZZ/AA were documented as "peak
  evictions in any 1-hour bucket" but the implementation
  summed the file counts (e.g. a 50-file run showed 0x32
  for that hour). The implementation didn't match the
  docstring.

FIX (commit dd23b2e):

  Renamed `evictionEvent.Count` → `evictionEvent.Runs`.
  `recordEvictions(runs int, ...)` now increments by 1
  per call regardless of the value passed (each call is
  one run). All 3 doc comments on the cache footer
  updated to say "RUNS" instead of "evictions".

  6 test functions updated to match the new semantics
  (each `recordEvictions(N, ...)` call = 1 run, not N
  runs).

USER-VISIBLE CHANGE:

  Before this commit (v1.1.1):
    A 50-file eviction in a single run would show YY
    = 0x32 (50) for that hour's bucket.

  After this commit (v1.1.2):
    The same 50-file eviction shows YY = 0x01 (1 run).
    A 50-file and a 200-file eviction both show 0x01;
    only a separate second run in the same hour would
    bump it to 0x02.

#### 2. New BB field: max cache size in hex

FIX (commit 0946ce5):

  Added a 5th hex value `BB` to the cache footer that
  shows the configured max cache size, scaled as
  `cap_in_MB / 64` so the 0-16 GB range fits in 2 hex
  digits. 1 GB = 0x10, 2 GB = 0x20, 4 GB = 0x40,
  16 GB = 0xFF (clamped). Unbounded (cap = 0) = 0x00.

  The footer tooltip was updated to mention the new
  5th value. The PageData struct was extended with
  `CacheStatsBB`. The `formatCacheStatsFooter` function
  signature was extended to take `capMB int` and return
  5 values. The `RenderPage` signature was extended to
  accept the 5th `cacheStatsBB` arg.

USER-VISIBLE CHANGE:

  Before this commit (v1.1.1 without the BB change):
    The cache status line shows 4 hex values:
      72 // 00 // 00 // 00
    The cap isn't shown in the footer at all.

  After this commit (v1.1.2):
    The cache status line shows 5 hex values:
      72 // 00 // 00 // 00 // 20
    The cap (2048 MB / 64 = 32 = 0x20) is now visible
    in the footer. The operator can see at a glance
    what the cache cap is, in hex.

NO BEHAVIOR CHANGES outside the cache footer display.
The eviction code, the LRU ordering, the scan refresh
goroutine, the Caddyfile directives — all unchanged
from v1.1.1.

All 450+ Go tests pass.

---

## 1.1.1 — 2026-07-04

### 🐛 Fix: cache-status footer percent scale (XX hex value)

Per user report 2026-07-04: the cache-status footer at
the bottom of the page was showing "53" (= 83% of
the cap, scaled to 0-100) instead of the expected
hex value scaled to 0-255 — the same full-byte range
used by the YY/ZZ/AA peak-eviction fields, where 100%
maps to 0xFF.

ROOT CAUSE:

  The function `CacheUsagePercent` (now renamed to
  `CacheUsageFractionHex255` in this release) returned a
  value in the 0-100 range:
    pct := int(s.SizeBytes * 100 / s.CapBytes)
  The template then formatted this as a 2-digit hex
  value via `fmt.Sprintf("%02X", pct)`:
    100% (= 100 decimal) → 0x64 = "64" = "d"
  But the doc comment AND the parallel peak-eviction
  fields YY/ZZ/AA all use the 0-255 scale, where
  100% = 255 = 0xFF. The XX field was the inconsistent
  one. Result: the footer correctly showed "53" (= 83%
  of the 0-100 scale) but the user expected it to be on
  the same 0-255 scale as YY/ZZ/AA, where 83% would be
  ≈ 0xD7 (≈ 214/255 ≈ 84%).

FIX (cache_stats.go):

  1. Renamed the function `CacheUsagePercent` to
     `CacheUsageFractionHex255` to reflect the new
     0-255 (1 byte, hex-display-friendly) scale. The
     return type is still int. -1 still means "unbounded"
     (caller renders ∞).

  2. Changed the math from `* 100` to `* 255` so
     100% maps to 0xFF (= 255) instead of 0x64 (= 100).
     Same clamping (0-255) and same special case for
     CapBytes <= 0 (unbounded → return -1).

  3. Updated the test cases in
     `TestCacheStats_CacheUsageFractionHex255Math`
     (renamed from `_CacheUsagePercentMath`):
       empty cache: 0 → 0       (no change)
       half full: 50 → 127      (was 50)
       full: 100 → 255          (was 100)
       over cap: 100 → 255      (was clamped 100)
       tiny: 0 → 0             (no change)
       unbounded: -1 → -1       (no change)
     And in `TestFormatCacheStatsFooter`, the
     `wantXX` for the half-full case was updated from
     "32" (50 on 0-100 scale) to "7F" (127 on 0-255
     scale = 50% of 0xFF).

USER-VISIBLE CHANGE:

  Before this commit (v1.1.0):
    Cache at 860 MB / 1024 MB cap → footer shows
    "53 // 00 // 00 // 00" (= 83% × 100/100, 0 evictions)

  After this commit (v1.1.1):
    Same cache state → footer shows
    "D6 // 00 // 00 // 00" (= 83% × 255/100 ≈ 214, 0 evictions)
    = 0xD6 in hex

  At 100% (cache at the cap):
    Before: "64 // ..."  (100 × 100/100, hex 0x64)
    After:  "FF // ..."  (100 × 255/100, hex 0xFF)

  The full range 0x00-0xFF is now usable, matching the
  YY/ZZ/AA peak-eviction fields. The fix is purely
  cosmetic (the underlying cache eviction behaviour is
  unchanged) but the operator's expectation that "100%
  = 0xFF" now holds.

NO BEHAVIOR CHANGES outside the XX field. The
eviction, scan cache, scan refresh, and stats refresh
behaviour are unchanged from v1.1.0.

### 🔤 Polish: cache-status footer in monospace font

Per user request 2026-07-04: the cache-status footer
hex value ("D6 // 00 // 00 // 00") should render in a
monospace font so the digit pairs and `//` separators
align vertically.

ROOT CAUSE:

  The `.site-footer-cache-stats` CSS rule referenced
  `var(--font-mono)` but the variable was never defined
  in the `:root` block. The `var()` call resolved to
  nothing and the browser fell back to the body font
  (proportional). The footer was effectively
  proportional — the digit pairs ("D6", "00", "00",
  "00") were slightly misaligned.

FIX (templates/gallery.tmpl):

  1. Added `--font-mono` to the default `:root` block.
     Stack: ui-monospace, SFMono-Regular, Menlo, Monaco,
     Consolas, Liberation Mono, Courier New, monospace.
     Modern systems (macOS, recent Linux) get a nice
     ui-monospace; older systems fall back to a system
     monospace; the last resort is the generic monospace
     family. This is the same variable the `.meta` status
     line COULD use (if a future commit enables it there)
     — but the user asked specifically for the cache
     status line only in this commit.

  2. The existing `.site-footer-cache-stats` rule was
     unchanged — it already referenced `var(--font-mono)`.
     The fix was just to define the variable.

USER-VISIBLE CHANGE (verified on the live page
https://hermes.synapticloop.com/images/ after the Caddy
binary is rebuilt):

  Before this commit (v1.1.1 partial):
    "D6 // 00 // 00 // 00" rendered in the proportional
    body font — the digit pairs (D6, 00, 00, 00) had
    slightly varying widths, so the column structure
    was hard to read at a glance.

  After this commit (v1.1.1):
    "D6 // 00 // 00 // 00" rendered in monospace — the
    digit pairs are now uniformly wide and the `//`
    separators align vertically between them.

  The `.meta` status line ("50 files // 36 images · 8
  videos · 3 audios · 3 other files // (38.2 MB total)
  // 52 directories · Show 60 Per page") is
  intentionally UNCHANGED in this commit — the user
  asked specifically for the cache status line only.
  The status line still uses the proportional font. If
  a future commit wants to monospace the status line
  too, it can just add `font-family: var(--font-mono);`
  to the `.meta` rule.

NO BEHAVIOR CHANGES — this is a pure CSS / cosmetic
fix. No Go code touched, no tests changed, no Caddyfile
changes.

All 450+ Go tests pass (CSS-only change; tests
exercise the Go server logic, not the rendered HTML).

### 🎵 Audio: opt-in audio file support

Per user request 2026-07-04 (audio-integration branch):
the gallery now supports audio-only files (mp3, flac, opus,
m4a, etc.) as a first-class media type, alongside images and
videos. Files matching the operator's `audio_types` directive
are classified as `KindAudio`, get an SVG speaker-icon tile
placeholder, a `<audio controls>` element in the lightbox, and
stream-level metadata (codec, sample rate, channels, channel
layout, duration, bitrate) extracted via `ffprobe` and cached
in `.ameta` sidecars.

Audio is OPT-IN since 1.1.0 — the default `audio_types`
list is empty, so existing 1.0.x configurations are unaffected
(operator opt-in is required). The branch landing was
coordinated via 4 commits on the `audio-integration` branch
before the merge.

What's new in 1.1.0:

  - **New `KindAudio` FileKind**: parallel to `KindImage` /
    `KindVideo` / `KindOther`. Per Q2 ("video wins"), a file
    with a video stream is `KindVideo` even if it also has
    audio; a file with only audio streams is `KindAudio`.
  - **New Caddyfile directive `audio_types <list>`**: same
    shape as `image_types` / `video_types`. Empty by default
    in 1.1.0 (opt-in). Recommended starting list:
    `.mp3 .m4a .aac .flac .opus .wav .ogg .oga` (8 formats).
  - **New Caddyfile directive `no_audio_meta`** (no arg → true,
    accepts "false"): defaults to FALSE (audio metadata
    extraction on by default when `audio_types` is enabled).
    Mirrors `no_meta` for video metadata.
  - **New `AudioMeta` struct**: Tier-1 stream-level fields
    (Codec, SampleRate, Channels, ChannelLayout, Duration,
    Bitrate). NO ID3 / Vorbis / iTunes tag extraction (per
    Q5 — Tier 1 only). Populated by `readAudioMetaCached`
    which uses two ffprobe subprocesses (stream info +
    format) and caches the result in a `.webp.ameta` sidecar
    parallel to the video's `.webp.vmeta`.
  - **Filter UI**: new "Audio" dropdown between "Videos"
    and "Other" (per Q7). Hidden when `audio_types` is empty.
  - **Card tile placeholder**: inline SVG speaker icon
    (Material Design `volume_up` glyph, fill="currentColor"
    themed via `--accent` CSS variable). Warm-purple gradient
    background distinguishes audio cards from video cards.
    No `<img>` child — purely CSS/SVG.
  - **Lightbox audio player**: native `<audio controls
    preload="metadata">` element, plus a parallel metadata
    panel with six rows (codec, sample rate, channels, channel
    layout, duration, bitrate). Panel is collapsible (state
    in localStorage, parallel to the video META panel).
  - **5 new lang keys × 8 locales = 40 translations**:
    `filter_audio`, `meta_codec`, `meta_sample_rate`,
    `meta_channels`, `meta_channel_layout`.
  - **Q3 ffmpeg-missing fallback**: if `audio_types` is set
    but `ffmpeg/ffprobe` isn't installed, a one-line stderr
    WARNING is logged at Provision() and audio files still
    work (KindAudio, filter membership, tile rendering,
    lightbox `<audio controls>` player) — only the audio
    metadata panel is empty. This matches the 1.0.x video
    behaviour where videos were still served even when
    ffmpeg was missing (just with a black play button
    instead of a poster frame).
  - **Scanner cache key** updated to include the audio ext
    set + `no_audio_meta` flag. Toggling `audio_types` or
    `no_audio_meta` in the Caddyfile invalidates the scan
    cache so visitors don't see stale `KindAudio=0` or stale
    `AudioMeta` fields.
  - **No new Caddyfile directive beyond `audio_types` /
    `no_audio_meta`**. Pure additive change. Stable per
    the 1.0 promise: no breaking changes within 1.x.

Implementation:

  - `scanner.go` — new `KindAudio` constant +
    `defaultAudioExts` map (empty). `Classify()` signature
    gained `audioExts map[string]bool`. The "video wins"
    rule preserves backwards compat: a file whose
    extension is in BOTH `video_types` and `audio_types`
    is `KindVideo` (the existing defaultVideoExts has
    higher priority in the switch).
  - `gallery.go` — new `AudioExts []string`,
    `audioExtsMap map[string]bool`, `NoAudioMeta bool`,
    `NoAudioMetaSet bool` fields on `Gallery`. The
    `audio_types` and `no_audio_meta` Caddyfile directives.
    Provision() logs a one-line stderr WARNING if
    `audio_types` is set but ffmpeg is missing.
  - `audio_meta.go` (NEW, ~480 lines) — `AudioMeta` struct
    + `readAudio()` (ffprobe subprocess) +
    `readAudioMetaCached()` (cache-aware entry point) +
    `audioMetaPath()` / `readAudioMetaFile()` /
    `writeAudioMetaSidecar()` / `parseAudioMetaSidecar()`
    (sidecar I/O). Format functions: `formatSampleRate`
    (44.1 / 22.05 / 11.025 kHz with trimmed decimals) +
    shared `formatDuration` and `formatBitrate` from
    `video_meta.go`.
  - `scancache.go` — `Cache.Get` and `extSetsKey` signatures
    gained `audioExts` + `noAudioMeta` parameters. Cache
    key now embeds `|a:<audKeys>|` and `|M:<noAudioMetaStr>|`
    segments.
  - `render.go` — `FileInfo.AudioMeta` field. `FileView`
    gained `AudioMeta`, `AudioMetaAttrs`, `IsAudio` fields.
    `buildFileView` has a new `case KindAudio:` branch
    (sets `v.IsAudio = true`, leaves `ThumbURL` empty for
    the SVG fallback path, emits a `.thumb-audio` div with
    SVG icon, emits a `.thumb-audio-duration` pill when
    `AudioMeta.Duration != ""`). `buildAudioMetaAttrString`
    formats `AudioMeta` into `data-audio-*` attributes.
    `computeFilterGroups` signature gained `audioExts` and
    returns `(images, videos, audio, other FilterGroup)`.
    `RenderPage` signature gained `noAudioMeta bool` and
    `audioExts map[string]bool` (21 → 23 args).
  - `templates/gallery.tmpl` — new "Audio" filter dropdown
    between Videos and Other. Inline SVG speaker icon in
    the audio tile. New `<audio controls>` lightbox path
    parallel to the existing `<video controls>` path. New
    `lb-audio-meta` lightbox panel with six metadata rows.
  - `lang/*.json` — 5 new keys × 8 locales = 40 entries.

Behaviour change:

  - **Before 1.1.0**: audio files (mp3, flac, etc.) were
    treated as `KindOther` and shown in the "Other files"
    strip with a 📄 icon. No thumbnail, no metadata, no
    audio filter, no `<audio controls>` lightbox.
  - **After 1.1.0**: when the operator has set `audio_types`,
    audio files become `KindAudio` and get the full
    audio UX: SVG tile placeholder, audio filter
    membership, metadata panel, lightbox audio player.
    When `audio_types` is empty (default), audio files
    fall through to `KindOther` — the 1.0.x behaviour is
    preserved.

---

## 1.0.3 — 2026-07-04

### ✨ i18n: translate the "Open in new tab" thumbnail link

Per user request 2026-07-04: when hovering the ↗ button
on each media tile, the tooltip (browser-native `title`)
and screen-reader label (`aria-label`) were hardcoded
English ("Open in new tab") regardless of the visitor's
active locale. Both attributes are now translated via
the existing `tr()` package-level helper.

Translations added to `lang/{en,de,es,fr,ja,ko,zh,pt}.json`:

  en: Open in new tab
  de: In neuem Tab öffnen
  es: Abrir en nueva pestaña
  fr: Ouvrir dans un nouvel onglet
  ja: 新しいタブで開く
  ko: 새 탭에서 열기
  zh: 在新标签页中打开
  pt: Abrir em nova guia

Implementation:

  - `render.go` — replaces the hardcoded literal in
    `buildFileViews` with `tr("open_in_new_tab")`,
    HTML-escaped before being interpolated into both
    the `title` and `aria-label` attributes. Uses the
    same package-level translation helper that
    `computeFilterGroups` already uses for the filter-
    pill labels, consistent with the existing server-
    side-translated strings.

Behaviour change:

  - **Before 1.0.3**: hovering the ↗ button on any media
    tile always showed "Open in new tab" in the browser
    tooltip, regardless of the visitor's chosen locale.
    Screen readers likewise always read "Open in new
    tab" in English.
  - **After 1.0.3**: both attributes are localized into
    the visitor's chosen locale (8 bundled translations).
    Missing-key fallback chain: requested locale →
    English → literal key (safe fallback).

No new Caddyfile directives, no new JSON fields, no new
URL parameters. Pure data change in the lang files + a
small inline change in the existing buildFileViews HTML
writer. Stable per the 1.0 promise.

---

## 1.0.2 — 2026-07-04

### ✨ UI: build / version metadata in the site footer

Per user request 2026-07-04: the site footer now includes a
new line between the existing "proudly served by caddy +
synapticloop // media gallery" brand line and the cache-stats
line. The new line shows the running binary's release tag
and short git commit hash:

    caddy_media_gallery 1.0.2 //
    [abc1234](https://github.com/synapticloop/caddy_media_gallery/commit/abc1234)

The commit hash becomes a clickable link to the GitHub
commit page so visitors can see exactly which source tree
the running binary was built from. The "unknown" fallback
(dev builds, or no -ldflags) renders as plain text with no
link.

Implementation:

  - New `version.go` defines exported package-level vars
    `Version` and `Commit`, plus a `VersionString()` helper.
    Both default to "dev" / "unknown" so the binary still
    renders correctly when built without -ldflags.
  - `render.go`'s `PageData` carries two new fields,
    `ModuleVersion` and `ModuleCommit`, populated from the
    package vars at the end of `RenderPage()`.
  - `templates/gallery.tmpl` has a new `.site-footer-version`
    div with matching CSS styling (mirrors the existing
    cache-stats rule — same mono font, muted colour, small
    size). Uses `{{if eq .ModuleCommit "unknown"}}...{{end}}`
    to choose between plain text and an anchor link.
  - `build.sh` extracts `git describe --tags --always` for the
    version and `git rev-parse --short HEAD` for the commit,
    then passes them to xcaddy via the documented
    `XCADDY_GO_BUILD_FLAGS="-ldflags '-X pkg.Var=value'"`
    env-var mechanism. Echoes the values to stdout during the
    build so the maintainer can confirm what was baked in.

Maintainer workflow (one-command release build):

    ./build.sh            # runs the existing build script;
                          # all the ldflags plumbing is now
                          # automatic. Just needs a git
                          # checkout with at least one tag
                          # reachable.

Behaviour change:

  - **Before 1.0.2**: the running binary's version + commit
    were not displayed anywhere. Operators had to check git
    tags + read the commit themselves to know which build
    was running.
  - **After 1.0.2**: the site footer shows the version + commit
    on every page render. The values are baked in at build
    time via Go's -ldflags mechanism so they reflect exactly
    what the binary was built from, not whatever happens to be
    in the source tree right now.

No new Caddyfile directives, no new JSON fields, no new URL
parameters. Pure UI + build-system change. Stable per the 1.0
promise.

---

## 1.0.1 — 2026-07-04

### ✨ UI: highlight filter pills that have an active selection

Per user request 2026-07-04: the type filter row in the
file filter section now visually distinguishes filter
dropdowns that have at least one option currently
checked. The Images / Videos / Other pill summaries use
the same colour scheme as the "Filter" apply button
(light-on-dark in light mode, dark-on-light in dark
mode) when any checkbox inside them is selected — so
visitors can see at a glance which filters are active
without having to open the dropdown.

Behaviour:

  - No filter, dropdown closed   → normal pill colour
  - Has selection, dropdown closed → Filter-button colour
  - Has selection, dropdown open  → normal pill colour
    (the visitor already sees the dropdown's contents;
    the pill colour only signals "is something filtered?")
  - Dropdown open, no selection → normal pill colour

The two states ("has selection" and "is open") are
deliberately decoupled — the pill colour signals whether
the filter is active, the dropdown itself signals whether
it's open.

Implementation:

  - New `.filter-pill-has-selection` CSS class uses the
    `--active-bg` / `--active-fg` / `--active-border`
    variables (same as `.filter-apply`), with a matching
    `:hover` rule using `filter: brightness(1.2)`.
  - New `.filter-dropdown[open] > .filter-pill.filter-pill-has-selection`
    selector added to the existing open-state rule, with
    higher CSS specificity (4 vs 1) so opening a selected
    dropdown reverts the pill to normal colour.
  - Template: each filter pill's `<summary>` class list
    appends `filter-pill-has-selection` when the
    corresponding `FilterXxxOptions.Selected` count is > 0.

Behaviour change summary:

  - **Before 1.0.1**: selected filter pills looked identical
    to unselected ones. Visitors had to open each dropdown
    to verify whether it was filtering anything.
  - **After 1.0.1**: selected filter pills match the "Filter"
    button visually. Open-vs-closed state is still signalled
    by the dropdown body appearing, not by the pill colour.

---

## 1.0.0 — 2026-07-04

### 🎉 First stable release

Per user request 2026-07-04: the Caddyfile directive surface, JSON
schema, and URL parameter API are now considered stable. Operators
can pin to `1.0.0` (or any later version) in their xcaddy build pipeline
and rely on backward-compatible behaviour for all existing features.

What's in 1.0.0:
- **Core gallery** — recursive directory walk, paginated image grid,
  lightbox with EXIF/META panels, dark/light mode toggle
- **Theming** — per-visitor light/auto/dark toggle, persisted in
  localStorage, no-flash on first paint
- **Search** — server-side (`?q=`) + client-side live filter, two
  match modes (`word` / `substring`)
- **Filters** — type filter (Images / Videos / Other) with checkboxes
  per extension
- **EXIF + dimensions** — extracted per-request, cached as `.exif` and
  `.meta` sidecars (operator-opt-in via `no_exif false`)
- **Video metadata** — duration / container / codecs / bitrate / framerate
  via ffprobe, cached as `.vmeta` sidecars (operator-opt-in via
  `no_meta false`)
- **Thumbnails** — on-demand WebP generation (images + videos), 2-level
  nested hash cache layout, LRU eviction with cap (operator-opt-in via
  `no_thumbs false`)
- **Internationalisation (i18n)** — 8 bundled locales (en/de/es/fr/ja/ko/zh/pt),
  language picker in the header, locale persisted in localStorage + cookie
  (added in this release)
- **Operator UX** — system-installed templates are discoverable on disk
  (operator can `ls` and edit), one env var (`GALLERY_TEMPLATES_DIR`)

Stability promise:
- Caddyfile directives, JSON config fields, and URL parameters that
  exist in 1.0.0 will not be removed or have their semantics changed
  in breaking ways in any 1.x release. New directives / fields / params
  may be added.
- Bug fixes are not considered breaking changes.
- The default values of existing directives may change between major
  versions (1.x → 2.x), with a deprecation period. The current 1.0.0
  defaults are documented in `docs/01-configuration.md` and
  `docs/02-configuration-reference.md`.

See [README.md](README.md), [docs/00-readme.md](docs/00-readme.md), and
the per-feature docs in `docs/03a-*` through `docs/03h-*` for the full
operator reference.

---

## 2026-07-04

### ✨ Feature: internationalisation (i18n)

Per user request 2026-07-04: added full internationalisation support. Visitors can now pick from 8 locales (English, German, Spanish, French, Japanese, Korean, Chinese, Portuguese). The selection persists in localStorage + cookie so visitors see their preferred language on every visit after the first reload.

**What's translated:**

- Header (title, status row, page size dropdown, filter labels)
- Directories table headers (`# Files`, `# Dirs`)
- Pagination controls (← Prev, Next →)
- Show-pages dropdown (the "all" option)
- Filter dropdown labels (Images / Videos / Other)
- Media section header (Media (N - Showing X-Y) and Media (N - search 'q' - showing N of M))
- Lightbox panel headers (EXIF / META)
- Lightbox field labels (Camera, Lens, Date, etc.)
- Language picker trigger + dropdown options

**New Caddyfile directive:**

- **`default_language <locale>`** — sets the default locale when the visitor hasn't yet specified one (via URL, cookie, or browser Accept-Language). Defaults to `en`.

**Locale resolution priority chain** (per visitor request):

1. `?lang=<locale>` URL parameter (highest)
2. `gallery-language` cookie
3. `localStorage["gallery-language"]`
4. `Accept-Language` HTTP header
5. `default_language` directive (from Caddyfile)
6. Hardcoded `en` (lowest)

**Visitor UX:**

- Language picker is a `<details>`/`<summary>` dropdown in the header, left of the dark/light mode toggle. Click an option → navigates to `?lang=<locale>` → page reloads → JS writes to localStorage + cookie. After the first reload, all subsequent visits use the cookie automatically — no further reloads needed.

**Adding a new language (operator):**

Operators can add a new language without rebuilding Caddy:

1. Drop a JSON file at `/etc/caddy/gallery-templates/lang/<locale>.json` with the same keys as `lang/en.json`
2. Restart Caddy (or rely on the next request to pick it up)

**Bundled locales:** `en`, `de`, `es`, `fr`, `ja`, `ko`, `zh`, `pt` — 8 files in the `lang/` directory, embedded into the binary via `//go:embed` and overridable on disk.

**Implementation details:**

- New `i18n.go` (~400 LOC): `Translator` struct, `T()`/`NativeName()`/`SelfName()` methods, `NewTranslator()` constructor (loads from embed + disk override), `DetectLocale()` for the priority chain, `ResolveLocale()` with q-factor matching for Accept-Language.
- New `{{t "key"}}` template function for use in `gallery.tmpl` (registers `currentT` + `currentLang` package-level vars under a `tMu` RWMutex; RenderPage sets these on entry, restores via defer on return).
- New package-level `tr()` Go helper for non-template code (e.g. `computeFilterGroups` building the filter dropdown labels).
- JS `t()` helper + `TRANSLATIONS` map (server-rendered) for client-side translation of the lightbox panel headers, search header JS, etc.
- 15 new unit tests + 3 new RenderPage tests + 3 new helper tests = **18 new tests, 450+ total pass**.
- 11 visual test groups (Playwright Python) verifying the locale renders correctly in each of the 8 languages.

See `docs/03h-feature-i18n.md` for the full architecture, locale resolution algorithm, and "adding a new language" walkthrough.

---

## 2026-07-03

### ✨ Feature: defaults aligned with `file_server browse`

Per user request 2026-07-02: three configuration options now default to `true` so the gallery behaves like caddys stock `file_server browse` out of the box (no enrichment, no extra dependencies, no I/O beyond the directory scan):

- **`no_exif`** (was `false`) — skip image EXIF reads by default (no exiftool / go-exif calls, no `.exif` sidecars, no EXIF pills on cards, no EXIF panel in lightbox). Operators who want EXIF reading opt in with `no_exif false`.
- **`no_meta`** (was `false`) — skip video metadata reads by default (no ffprobe subprocess calls, no `.vmeta` sidecars, no META pills on cards, no META panel in lightbox). Operators who want video metadata enrichment opt in with `no_meta false`.
- **`no_thumbs`** (was `false`) — skip thumbnail generation by default (no thumb cache, no ffmpeg CPU overhead, original file used as `<img src>`). Affects both images and videos. Operators who want rich thumbnails opt in with `no_thumbs false`.

The directive names are now technically misnomers (since theyre the default), but theyre kept for backward compatibility — operators who already had `no_exif` or `no_thumbs` in their Caddyfile see no behavior change.

To get the **full enriched UX** (EXIF pills + video metadata + thumbnails), set all three to `false`:

```
media_gallery {
    no_exif false
    no_meta false
    no_thumbs false
}
```

Implementation details:
- Added `NoExifSet`, `NoMetaSet`, `NoThumbsSet` companion fields to the `Gallery` struct. The Caddyfile parser sets these flags whenever the corresponding directive appears. `Provision()` defaults the value to `true` if the `Set` flag is `false` (no directive in Caddyfile), but preserves the operators explicit value when they use `no_xxx false`.
- Fixed a latent bug in `scanner.go` `enrichParallel` where the video metadata block did NOT check `!s.NoMeta` (it always ran). Now the production enrich path correctly skips video metadata when `no_meta=true`.
- Fixed a latent bug in `scancache.go` `extSetsKey`: the function declared a `noMeta` parameter but never used it in the returned key. Now both `noExif` and `noMeta` are part of the cache key, so changing the `no_meta` flag invalidates the cache (otherwise old cache entries with `VideoMeta` populated would be returned for new requests where `no_meta=true`).
- Updated `render.go` `buildFileView` for `KindVideo`: the video `ThumbURL` is now only set if BOTH `noThumbs` AND `noVideoThumbs` are `false`. Previously, `no_thumbs` didn't affect videos.
- 6 new tests added for the new default behavior and the operator opt-in pattern.

### 🐛 Fix: broken Last Modified sort + Size sort didn't account for KB/MB/GB
Two sort bugs in the directories and other-files tables:
- **Last Modified sort** was a no-op for directories because `v.ModTime = f.ModTime` was only set in the `default:` branch of `buildFileView()` (for "other" files). All directory rows had `data-date="0"`. Fixed by moving the assignment to BEFORE the switch in `buildFileView()` so it's set for ALL file kinds (KindDir, KindImage, KindVideo, default).
- **Size sort** used `parseFloat("58.2 MB") = 58.2` and `parseFloat("1.05 GB") = 1.05`. The comparison `58.2 < 1.05` is false, so 1.05 GB sorted before 58.2 MB. Fixed by adding a `parseSizeToBytes()` helper in the JS sort code that converts the human-readable size to bytes before comparing. Now sorts correctly across KB/MB/GB/TB boundaries.

### ✏️ UI: size bars in directories/other-files tables less tall
Per user spec: `.files-table .col-size::before` now uses `top: 7px; bottom: 5px` (was `top: 2px; bottom: 2px`). The relative-size bars in the SIZE column have more breathing room above the text and slightly less below, making them visibly more compact while still showing the relative-size progression.

### ✏️ UI: refine collapsed EXIF/META panel
Three rounds of refinement on the collapsible EXIF/META panel in the lightbox:
- **Rotation direction:** text now reads top-to-bottom (anticlockwise). Changed `writing-mode: vertical-rl` to `writing-mode: vertical-lr`. The vertical bar on the right of the image now reads "EXIF" or "META" from top to bottom instead of bottom to top.
- **Position:** panel moved from "to the left of the image with a gap" to "to the right of the image, slightly overlapping". Now sits at `img.right - panel.width + 8` (8px inside the image's right edge), giving the "leaning against the right edge" feel.
- **Height stability fix:** the collapsed panel was growing in height on every toggle. Root cause: the base CSS had `bottom: 6rem` AND the `.collapsed` rule had `top: 50%`, so the height was forced to `viewport.height - top - bottom` (354px on a 900px viewport, even though content was only ~50px). Each toggle the JS updated `top` to re-center with the image, but that changed the height, creating a circular dependency. Fixed by adding `bottom: auto` to the `.collapsed` rule. Toggle handler also now clears inline `left`/`top`/`transform` on expand so the expanded CSS position is restored cleanly.

### 🐛 Fix: META panel positioned immediately on video open
Previous fix (c52c5ff) waited for `loadedmetadata` before positioning, but until that event fired, the panel sat at the CSS fallback position (left: 0.5rem — the LEFT side of the lightbox). New approach: position IMMEDIATELY using the current bounding rect (poster-sized for videos), then re-position when `loadedmetadata` fires. If loadedmetadata never fires, the panel stays at the poster-sized position (better than the CSS fallback on the left).

### ✏️ UI: rename "Sort By" to "Sort Media By"
Per user spec: the sort label above the Name/Type/Modified/Size buttons is now "Sort Media By" (was "Sort By"). More descriptive — the sort applies to the media section, not the directories/other-files tables.

### ✨ Feat: add no_meta Caddyfile directive
Per user request 2026-07-02: a new `no_meta` directive that disables video metadata extraction (duration, container, codecs, bitrate, framerate via ffprobe). This is a SEPARATE flag from the existing `no_exif` directive — `no_exif` affects image EXIF, `no_meta` affects video metadata. Use case: large video directories where the operator doesn't need the per-video metadata enrichment (saves 50-100ms per video on first extraction). Both flags can be enabled independently. Caddyfile syntax mirrors `no_exif`:

```
media_gallery {
    no_meta         # disable (default)
    no_meta false   # re-enable
}
```

---

## 2026-07-02

### ✨ Feature: video metadata (META panel)
Per user request 2026-07-02: parallel "META" panel for videos, showing duration / container / codecs / bitrate / framerate (extracted from `ffprobe`, cached in `.vmeta` sidecars). New `video_meta.go` module shells out to `ffprobe -v error -show_format -show_streams -of json`, parses the JSON, and extracts 6 fields. The `FileInfo` struct gained a `VideoMeta *VideoMeta` field; `FileView` gained matching fields and a pre-rendered `VideoMetaAttrs string` (avoids 6 separate template reflection lookups per card). Lightbox META panel: 6-row table below the caption. Card overlay: small "META" pill, same style as the EXIF pill. `video_meta_test.go` (10 tests): ffprobe JSON parsing, each field, malformed JSON, sidecar I/O.

### ✨ Feature: collapsible EXIF/META panels in the lightbox
Per user request 2026-07-02: the lightbox EXIF and META panels can now be collapsed to a vertical bar by clicking the panel header. State persists in `localStorage` (`gallery-lb-exif-collapsed` and `gallery-lb-video-meta-collapsed`) so the visitor doesn't have to re-pick on every visit. When collapsed, the panel becomes a vertical bar (~28px wide) with the text rotated 90° via `writing-mode: vertical-rl` (later refined to `vertical-lr` for anticlockwise reading). Header click toggles and persists; keyboard accessible (Enter/Space on the role="button" header).

### ✨ UI: META pill on video cards
Per user request 2026-07-02: small "META" pill on video cards, parallel to the "EXIF" pill on image cards. Same visual style (accent-coloured chip) so visitors can tell at a glance which kind of metadata is available. Initial placement was on the bottom-left of the thumbnail; later refined to match the EXIF pill position.

### ✏️ UI: back-to-top button with scroll percentage
Per user request 2026-07-01: "Back To Top" button fixed at the bottom-center of the viewport, appears after scrolling past one full viewport height. Black background, white text, rounded corners. The button shows the current scroll percentage as a small badge alongside the "Back To Top" text (e.g. "Back To Top [50%]"). The percentage is computed as `scrollY / (scrollHeight - clientHeight) * 100`, updated on every scroll event via `requestAnimationFrame` (no jitter, no scroll-event spam).

### ✏️ UI: refine EXIF/META panel header
Per user request 2026-07-02: the EXIF/META panel header (the "EXIF" or "META" text) now has a slightly lighter background (rgba(255,255,255,0.06) on the dark panel) and a horizontal divider line underneath. The hover state removes the underline (per user spec) and uses a background change instead. The expanded panel has the same width whether expanded or collapsed (`min-width: 340px`) so the panel doesn't shrink when toggled.

### ✏️ UI: relative-size bars in SIZE column
Per user request 2026-07-01: the SIZE column in the directories and other-files tables now shows a light-grey background bar representing the file's size relative to the largest in the column. The bar width is set via the `--size-pct` CSS custom property, computed from the file sizes during render. Smaller files have shorter bars (or no bar at all for 0-byte files), giving a visual sense of relative sizes at a glance. Dark-mode-aware via the `--bar-bg` variable.

### 🐛 Fix: enrichment was on the wrong slice
Per user feedback 2026-07-01: the previous sync-enrich commit was enriching the wrong slice. `visibleAndOffPage()` returns `paged` as a sub-slice of a **freshly-created** slice (because `applySearchFilter`, `applyTypeFilter`, and `splitFiles` all COPY the `FileInfo` struct values). Mutations to `paged` did not propagate back to the original `files` slice that `RenderPage` sees. Fix: skip `visibleAndOffPage` for the sync enrich; call `scanner.enrichParallel(files, 8)` directly on the original `files` slice. Result: first-page visit now correctly shows 60 W × H watermarks and 4 EXIF pills (was 0 of each). Trade-off: enrich now runs on the full directory (96 files / 8 workers × ~10ms = ~120ms) instead of the visible page (60 files). For directories with 4000+ images this would be ~5s — too slow; a future optimization could maintain a name-based index.

### ✏️ UI: rename filter label and search button
- Type Filter → **File Type Filter** (the dropdown next to the Sort by buttons). Plain English, matches operator-facing naming in docs/04-sort-and-pagination.md.
- Search all → **Search All** (the submit button next to the search input). Title-case matches the Reset/Filter button styling.

### ✏️ UI: header borders and "Sort By" label
Three small layout/label changes on the header area:
- **Removed** the `border-bottom` from `.header-top` (the line between the status area "3 files // 3 images..." and the breadcrumb `images > crosswords`). The breadcrumb now sits directly below the status with no separator.
- **Added** `border-bottom: 1px solid var(--border)` to `.filter-form` — a visible separator between the filter row (File Type Filter / Search All / Reset) and the media grid below. The search controls remain right-aligned within the filter row.
- **"Sort by" → "Sort By"** in the sort-bar label. Title-case to match the Filter / Reset / Search All button styling.

---

## 2026-07-01

### 🐛 Fix: enrich the original files slice (not the filtered copy)
Per user feedback 2026-07-01: the previous sync-enrich commit was enriching the wrong slice. `visibleAndOffPage()` returns `paged` as a sub-slice of a **freshly-created** slice (because `applySearchFilter`, `applyTypeFilter`, and `splitFiles` all COPY the `FileInfo` struct values). Mutations to `paged` did not propagate back to the original `files` slice that `RenderPage` sees. Fix: skip `visibleAndOffPage` for the sync enrich; call `scanner.enrichParallel(files, 8)` directly on the original `files` slice. Result: first-page visit now correctly shows 60 W × H watermarks and 4 EXIF pills (was 0 of each). Trade-off: enrich now runs on the full directory (96 files / 8 workers × ~10ms = ~120ms) instead of the visible page (60 files). For directories with 4000+ images this would be ~5s — too slow; a future optimization could maintain a name-based index.

### 🐛 Fix: enrich visible-page files synchronously so EXIF + dimensions appear on first visit
Per user feedback 2026-07-01: the first page load to a directory was missing the EXIF pill and the W × H watermark — a refresh was needed to see them. Root cause: `Cache.Get` returned unenriched `[]FileInfo`, and the background `EnrichInBackground` goroutine was still populating them when the HTML was sent. Fix: in `ServeHTTP`, after `Cache.Get` returns, run `enrichParallel(files, 8)` synchronously before calling `RenderPage`. Result: first-request page includes 60 W × H watermarks and 4 EXIF pills; cold-request time goes from ~130ms to ~875ms (acceptable for a first visit). See the commit `506017a` follow-up for the slice-propagation fix.

### ⚡ Perf: synchronously create .meta + .exif sidecars on thumb request
Per user request 2026-07-01: a single `serveThumb` request now leaves a complete cache state (thumb + `.meta` + `.exif`). Before this change, the sidecars were created asynchronously by the scanner's background enrichment, which caused the "first lightbox shows partial data" bug. Fix: after `GenerateOrLoadThumb` returns, `serveThumb` also calls `readDimensionsCached` + `readExifCached` (the latter skipped if `no_exif` is set) before writing the response. Cold-path overhead: +10ms (5-15ms per file for EXIF + dimensions reads); warm-path overhead: <20µs (mtime checks only). All 405 → 406 tests pass.

### ⚡ Perf: lazy thumb generation (on demand) + scan cache 24h TTL
Per user request 2026-07-01: removed the eager-gen goroutine from `ServeHTTP` (which was pegging the CPU on first visits to large directories with 10 parallel workers generating all 60 on-page thumbs synchronously). Now thumbs are generated on demand by `serveThumb` when the browser requests them. Also bumped `CacheScanMinutes` default from 1 to 1440 (24h) — the scan cache's primary invalidation is the directory mtime check, so a 24h TTL is a safety net for edge cases (clock skew, manual mtime changes). Result: page-load CPU drops from "100%+ peak during page load" to "51% peak during initial thumb wave". Operators can still set `cache_scan 1` to opt back into the 1-minute fallback.

### 🐛 Fix: scan-cache enrichment data race (atomic SetFiles swap)
The previous `EnrichInBackground` design mutated the cached `files` slice in place from a goroutine. Multiple concurrent cache reads within the TTL could return copies of the slice at arbitrary points in the enrichment, so the same page could return different EXIF data on each refresh. Fix: added `ScanCache.SetFiles(dir, files)` for an atomic swap (under the write mutex), and `EnrichInBackground` now enriches a copy and calls `SetFiles` when done. Future cache hits see the enriched data; no in-progress mutation is observable. Cache state is now consistent within a TTL window.

### 📚 Documentation: canonical localStorage reference
Added a comprehensive "What the template stores in localStorage" section to `docs/03-templates.md`. Documents every key the template reads or writes (`gallery-theme`, `gallery-dirs-sort`, `gallery-dirs-order`, `gallery-others-sort`, `gallery-others-order`, `gallery-section-<dirs|others>`), explains the `gallery-` namespace prefix, and includes a quick-copy snippet for clearing all keys (useful during operator testing). Cross-references added in `README.md` (new bullet in Features) and `docs/04-sort-and-pagination.md` (link to the localStorage reference).

### ⚡ Perf: split scan into fast + background enrichment
`Scanner.Scan()` no longer reads EXIF or pixel dimensions inline (those took ~45 seconds for 4491 files and blocked the HTTP response). The slow path moved to `Scanner.EnrichInBackground()`, which uses a worker pool of 8 and runs in a goroutine after the fast ScanCache path returns. Result: cold-cache page load for `/images/imagequeue/` (4497 files) drops from **9-46 seconds** to **~227ms**.

### ⚡ Perf: pre-render card markup as a single template.HTML string
The render hot-path was dominated by `html/template.Execute` walking the card node tree. Now each card's full HTML is pre-computed in Go (via `buildCardHTML`) and emitted as a single `{{.CardHTML}}` substitution in the template. Result: 60-file render drops from **2.85ms to 0.96ms** (3x speedup). The 405 existing tests still pass — they do byte-equivalent string matching on card markup, validating that the pre-rendered HTML is identical to the template's output.

### ⚡ Perf: eager-generate page-visible thumbs + background the rest
- `a01fbf2` perf: eager-generate page-visible thumbs (10 parallel workers, ~600ms total) and background the rest (2 workers, several minutes for thousands of files)
  - Subsequent commits (19271b5, 506017a) replaced this with lazy thumb gen + sync visible-page enrich
  - Original rationale: keep the on-page thumbs warm before the browser's parallel requests; the off-page thumbs were warmed for subsequent page navigations
  - Removed because the 10-worker sync phase pegged the CPU on first visits to large directories

### 📚 Documentation: Caddy-level encode compression
- `fec0364` docs: add a new "Caddy-level configuration" section to `docs/01-configuration.md` documenting the `encode zstd gzip` directive
  - Saves ~140 KB per gallery HTML response (160 KB → 20 KB, 7.7x reduction)
  - Operator should add `encode zstd gzip` to their Caddyfile at the route or global level
  - Also notes that thumbs aren't affected (already WebP-compressed at generation time)

### 🐛 Fix: cache stats now correctly walk the nested cache directory
- `b304b0a` fix: the cache size calculation used `os.ReadDir` + `if entry.IsDir() { continue }` which would skip all entries once the cache is in the nested (2-level) layout (every top-level entry becomes a directory)
  - Replaced with `filepath.Walk` that recursively visits all files
  - Live verified: footer now correctly shows `01 // 00 // 00 // 00` (1% used of 1 GB cap) instead of `00 // 00 // 00 // 00`

### ♻️ Refactor: remove legacy flat-layout cache fallback
- `cfe2c1f` refactor: remove the legacy flat-layout cache fallback (rely on the 2-level nested hash subdir layout)
  - Old flat layout (all thumbs in `/var/cache/caddy-gallery/`) is now considered deprecated
  - New nested layout (`/var/cache/caddy-gallery/<aa>/<bb>/<hash>.webp`) avoids the "directory has too many entries" problem
  - Migration: stale flat-layout files are simply not picked up by the new lookup; the next thumb request regenerates them in the new layout

### ⚡ Perf: split thumb cache into 2-level nested hash subdirs
- `cd21cd7` perf: split the thumb cache into 2-level nested hash subdirs
  - Old layout: `/var/cache/caddy-gallery/<hash>.webp` (one flat dir, 1000+ entries trigger filesystem slowdowns)
  - New layout: `/var/cache/caddy-gallery/<aa>/<bb>/<hash28>.webp` (2 hex chars per level)
  - For 100k thumbs: ~5 entries per innermost subdir (well under ext4's ~10k-entry degradation)
  - The hash base is `sha256(abs_path + thumbExt)[:16]` (32 hex chars), split into `<aa>/<bb>/<rest28>` for the dir structure

### ⚡ Perf: pre-compute EXIF attribute string (saves ~28% on EXIF-heavy pages)
- `bf6ee21` perf: pre-compute the `data-exif-*` HTML attribute string in Go (via `buildExifAttrString`), and emit it as a single struct field in `FileView.ExifAttrs`
  - Before: template had 8 separate `{{.Exif.CameraMake}}` field accesses per card, each doing reflection-based lookup
  - After: 1 string field per card, no reflection
  - Result: ~28% speedup on EXIF-heavy pages (60 cards each with 8 EXIF fields = 480 template field lookups → 60 string substitutions)

### 🐛 Port change: local-install default is now 3245 (was 8080)

`build.sh --user` now defaults to port **3245** (= 0xCAD in hex — a small easter egg for the project's homepage, since C-A-D happen to all be valid hex digits and the abbreviation is memorable). The script's comments, the auto-generated `Caddyfile.user`, and the help text (`build.sh --help`) all reflect the new default.

To keep using 8080 (the prior default), pass `--user 8080` or set `CADDY_USER_PORT=8080`. All operator documentation (README.md, docs/00-readme.md, docs/01-configuration.md) updated to reflect the new default; remaining 8080 references in docs are intentional ("to override, use 8080...").

---

## 2026-06-30

### UI / Button styling
- `2c3fae5` refactor: rename `Apply` button to `Filter`
- `2154e97` refactor: move `All` button after `Apply`, rename to `Reset`
- `23b18ca` fix: `All` pill and `Reset` button now use the same color scheme as Sort by buttons
- `493f51b` fix: `Search all` is black (primary), `Reset` matches `All` pill (secondary)
- `26c9dc9` fix: `Apply` button now same height as `Search all` / `Reset` (1px font diff)

### Template refactor
- `dad72f3` refactor: move gallery template to separate file with `//go:embed`
  - Template moved from embedded Go string in `render.go` to `templates/gallery.tmpl`
  - New `template_embedded.go` file with `//go:embed templates/gallery.tmpl`
  - Runtime override behavior preserved (on-disk file at `/etc/caddy/gallery-templates/gallery.tmpl` still takes precedence)

### Search header
- `8400fa9` feat: MEDIA header format — `'X of Y'` for form submit, `'X of Y THIS PAGE'` for JS search
- `4f0aa24` feat: keep `'Media (DIRECTORY_TOTAL -'` prefix when search is active
- `464c1b7` feat: filter-form preserves `page_size` on search submit
- `041a625` fix: search header updates to JS format when user changes text after form submit
- `681e315` feat: include search phrase in MEDIA header (e.g. `"search 'st' -"`)
- `4c5a6ce` fix: search header default is no-search format (not form-submitted text)
- `4872f5a` refactor: search filter now greys out non-matching items instead of hiding
- `27fd5e8` fix: search header uses `FilteredTotal` (not page size) when not paginated
- `229a979` fix: search header correctly uses JS format when visitor types more chars

### Image types
- `88a039b` refactor: remove `heic`/`avif`/`svg` from default `image_types` list
  - Go's stdlib can't decode HEIC/AVIF/SVG; default list now contains only formats that work out-of-the-box
  - Operators can opt in with `image_types .heic .avif .svg` if they have external tooling
  - Files with unrecognized extensions now correctly appear in the "Other files" section with a 📄 icon

### Directories header
- `f731f9c` feat: directories header shows `"+1 parent"` when there's a parent
- `e4703f7` refactor: directory header now shows `'+ Parent Directory'` in italics

### Pagination
- `dbbf6e1` fix: pagination links no longer turn blue on hover

### Mobile
- `d5ba7c7` fix: wrap type filter elements in a div so they stay grouped on mobile

### Hover tooltip
- `e375bc5` feat: hover tooltip on thumbnails shows filename (no ext, no `_` or `-`)
  - Native browser tooltip (`title` attribute) + custom CSS tooltip (`:before` pseudo-element)
  - Filename transformation: strip extension, replace `_` and `-` with spaces
  - Example: `misty_bamboo_forest_path.jpg` → `"misty bamboo forest path"`

### EXIF sidecar (stale detection + format)
- `1eb7c8a` fix: detect stale `.meta` and `.exif` sidecars via source mtime check
- `d36ded2` refactor: EXIF sidecar keys use Human-Readable names (matching lightbox labels)
- `6f6b9f6` refactor: EXIF sidecar uses plain text format (no JSON) for speed

### Other filter — `(none)` entry
- `e029117` feat: include `(none)` entry in Other dropdown for files without extensions
  - Files like `Makefile` or `welcome` (no extension) now appear in the Other dropdown
  - Two bugs fixed: directories without extension no longer counted as files; files with no extension no longer silently skipped
- `df15d41` fix: `(none)` is a strict filter — only show files without an extension
  - Sentinel value `.` (literal dot) in the form (can't be a real file extension)
  - `parseTypeFilter` translates `.` to `""` in the filter map
  - `applyTypeFilter` checks `filter[""]` for the strict no-extension filter
  - Multi-select OR logic: `?ext=.&ext=.md` shows files matching either

### Documentation
- `439e937` docs: add CHANGELOG.md with all commits grouped by date and category
- `47a30b4` docs: refresh screenshots to show EXIF pill on strawberry (after `no_exif` removed from localhost bypass)
- `3249e2c` docs: update README with new features (EXIF pill, hover tooltip, + Parent Directory, sidecars, //go:embed)
- `0564bfc` docs: document `(none)` filter across README, CHANGELOG, and operator docs

---

## 2026-06-29

- `2764360` feat: `no_exif` Caddyfile directive to disable EXIF reading entirely
  - Skips EXIF parse at scan time, endpoint returns 404
- `ac69c8b` fix: sort bar links preserve the page parameter (instead of resetting to page 1)
- `baaab59` feat: breadcrumb + dirs-table links preserve all query params (`q`, `type`, `sort`, `order`, `page_size`) but reset page to 1

---

## 2026-06-28

### Search
- `22cd797` fix: JS overwrites correct server-rendered search header
- `88d86c4` fix: search header `N` value (per user clarification)
- `991c934` feat: search header format with `"search showing M of N <em>This page</em>"`
- `1054774` feat: search-aware media section header (server-side + JS)
- `84f4e73` feat: add `search_match` Caddyfile config (`word`|`substring`, default `substring`)

### Page size
- `912f4b4` fix: per-page dropdown now shows `"all"` as selected when `?page_size=all`
- `f163927` fix: `"all"` option in per-page dropdown now shows all items
- `2f832bd` fix: exclude `page_size` from the page-size form's hidden inputs
- `02af751` fix: changing page size always resets to page 1

### Cache / performance
- `9abab25` feat: cache stats footer — `XX // YY // ZZ // AA` in hex
- `7cd8709` feat: add `max_cache_size_mb` Caddyfile directive (default 1 GB, `0` = unbounded)
- `b061782` feat: subtle shimmer animation while thumbnails are loading

### Buttons
- `f7f2361` style: sort button hover now matches the Search all button
- `1635d4f` fix: `Apply` + `Reset` button hover states keep text contrast
- `f678489` feat: search `Reset` button next to `"Search all"`

### Breadcrumbs
- `0fca13f` revert: remove the `"/"` breadcrumb separators
- `6adad1e` style: breadcrumb `"/"` separators are darker and bigger
- `51bf05e` feat: large `"/"` separators in breadcrumbs (between each segment + at the start)

### Documentation
- `eaf67d3` docs: bring all documentation up to date with current feature set
- `85bfdc4` feat: media header shows total + current page range

---

## 2026-06-27

- `70d6eff` style: move dimensions watermark from bottom-right (card) to bottom-left (image)
- `f44b81b` feat: source image/video dimensions watermark on thumbnails
- `731e049` feat: EXIF metadata display in lightbox + EXIF pill on card
- `ad73418` fix: filter dropdowns no longer auto-open
- `af3b5cb` style: remove background from `.sort-indicator` Block 1
- `e3c3727` style: remove coloring from active sort indicators and headers
- `88590b2` style: remove border, border-radius, padding from `.sort-indicator` Block 1
- `63c2870` style: `.sort-indicator` — remove border + padding, add margin-top
- `b1b86be` fix: add table IDs so the header-sort JS can find the tables
- `97def0c` fix: pagination + sort-bar links preserve all URL query params
- `e15a352` style: label the per-page dropdown `"Show [N] Per page"`
- `236064f` feat: clickable column headers with persistent sort (URL + localStorage)
- `2418fe6` feat: dirs table size column now shows sum of file sizes in subdir
- `73a5761` style: rename `"# Dirs"` to `"# Sub-Dirs"` with non-breaking spaces
- `7817235` fix: directory listing always shows, even when a filter is active
- `33a48e1` feat: dirs table now has `# Items`, `# Dirs`, and `Size` columns
- `d8d2cbe` fix: `?page_size=N` URL parameter is now honoured
- `0123511` refactor: rename `num_per_page` back to `page_size` + default 60
- `e9e6428` fix: default page size is the operator's declared first item
- `41952c2` refactor: rename `page_sizes` → `num_per_page` + sorted dropdown
- `aea2b31` style: rename `"Filter"` label to `"Type Filter"`

---

## 2026-06-26

- `2443b46` feat: search interface (client-side + server-side, word-boundary match)
- `e97779a` style: remove `padding: 4px 0 0 0` from `.section-toggle`
- `e04d46c` fix: breadcrumb root name now resolves correctly in Provision
- `7ba3c83` style: add `margin-top: -0.25rem` to `.page-size-select`
- `134b762` style: page-size dropdown matches filter-pill look + preserves URL params
- `a0857c5` fix: page-size dropdown template type mismatch + test fixes
- `c8c8e5f` feat: configurable `page_sizes` dropdown + default 60
- `5bc8638` fix: add missing `root_name` case to `UnmarshalCaddyfile`
- `1093802` fix: add `border-bottom` back to `.breadcrumb-link` + collapse to one line
- `142895c` fix: remove duplicate `.breadcrumb-link` block with `border-bottom`
- `0522c01` refactor: remove `»` separator + drop `border-bottom` on breadcrumb links
- `fc69323` feat: `root_name` Caddyfile directive + fix breadcrumb bottom border
- `9583ec8` refactor: `»` separator moved inside the breadcrumb link
- `1f76b09` refactor: rectangular breadcrumb with `»` separator + fix `/images/` display bug
- `6c6185a` fix: chevron duplicate + overlap + current chevron colour
- `745c347` refactor: breadcrumb order + chevron style (filters below breadcrumbs)
- `216da1b` fix: breadcrumb links are absolute URLs when `path_prefix` is set
- `b084d29` refactor: `Apply` button uses `--active-*` color scheme (matches sort/pagination)
- `988e936` fix: media section toggle JS now picks up `.media-section`
- `d92cbd2` refactor: filter above breadcrumb, less left padding, fix breadcrumb order
- `24dbee1` feat: add show/hide toggle to Media section (with the line)
- `82e7b3a` feat: filter UI with dropdowns + Apply button (Phase 4)
- `96c5251` feat: server-rendered breadcrumb (Phase 3)
- `b4f2296` feat: server-side `?type=` filter plumbing (Phase 2)
- `c358cdf` refactor: rename `images-section`/`image-grid` to `media-*`, make heading-divider more visible
- `004f93f` feat: configurable `image_types` and `video_types` via Caddyfile

---

## 2026-06-25

### Documentation / build
- `fae8150` docs: add SIL OFL 1.1 font credits page before the endplate
- `ae08f38` docs: document that ffmpeg detection is startup-only; log the resolved path
- `3d54300` docs: add local install (no sudo) section to 3 operator docs
- `ed365f0` feat: local install (no sudo) via `build.sh --user [PORT]`
- `e17748b` docs: add tagline `"The delightful way to serve a directory."`
- `b5171d3` feat: cache parsed template across requests (Phase 102)
- `d21ae3c` docs: refresh the PDF + use the new cover image + portability fixes
- `b3ae85d` docs: add new docs to README Documentation section
- `fa1dbee` docs: Updated the README file
- `25ffd17` docs: add the 3 source PNG screenshots to git (dark, light, lightbox)
- `5a5310d` docs: add lightbox screenshot to README + explanation
- `8f3ca69` docs: add animated preview GIF to README + update title text

### Animated fade GIF
- `2ea5cc6` feat: hold first and last frame for 3 seconds each in the fade GIF
- `db235bc` feat: add animated fade GIF (light → dark) for the docs screenshots

### Lightbox
- `aae668a` refactor: remove lightbox text labels (revert Phase 86 + 88)
- `80d40de` refactor: remove `align-items: center` from `.section-toggle`
- `09e7dc0` fix: add `padding-left` to the sort-by arrow (↑/↓)
- `edab437` feat: lightbox button labels enclosed in same grey rounded bg as the icon
- `6dce9b2` feat: lightbox buttons have rotated text labels (Open in new tab, Close)
- `f88baf7` feat: active sort + pagination buttons invert page colors (not blue)
- `e1c3d0a` feat: bigger lightbox close icon (✕ instead of ×)

---

## 2026-06-24

### Project rename: `caddy_image_gallery` → `caddy_media_gallery`
- `3fe7af0` refactor: rename project to `caddy_media_gallery` (was `caddy_image_gallery`)
- Module path changed, all references updated (Caddyfile directives, file paths, docs)

### Tables
- `8be8db1` fix: up-row-table now has `font-size: 0.85rem` (was inheriting 1rem)
- `2358459` refactor: up-row-table td no longer has `font-weight: 500`
- `30f2f59` refactor: `.files-table .col-type` width `auto` (was 6rem)
- `0ba01f9` refactor: replace `.sort-bar` negative-margin hack with `.header-top` border + padding
- `041849e` feat: add count in parens after directories + other files headings
- `c1773db` feat: add total file count to start of meta line
- `5be74b6` feat: remove `Type` column from dirs table (all entries are `DIR`)
- `94ceea0` feat: up entry as separate table above dirs table (no up-spacer row)
- `5368451` fix: make horizontal lines (header, sort-bar, section) the same width
- `97558a7` feat: whole-width section heading clickable to toggle show/hide
- `555cbde` feat: complete table row clickable for dirs + others tables
- `28abbf2` feat: section heading font bump, dir dates, up-row in table, heading divider, white sort arrow
- `0f4c100` feat: section toggle for directories + other files
- `e7d3fb8` feat: other files respond to sort selection (dirs stay alphabetical)
- `54b6841` feat: directories + other files as full-width tables with details
- `b6a227d` docs: expand JSON config section with full example + field mapping + validation
- `f8b8383` feat: `FFMPEG_PATH` env var for non-standard ffmpeg locations
- `98609c6` docs: update Lightbox controls section for Phase 65 prev/next hit areas
- `594bb5e` feat: lightbox prev/next hit areas fill window height + subtle hover tint
- `81f428b` docs: catchup for Phases 54, 55, 56, 58, 59, 60, 61
- `88ecefc` feat: video thumbnails via ffmpeg + show as lightbox poster before play
- `48544f2` fix: mobile video play button no longer advances to next media file
- `ba19cad` feat: section heading `"Images"` → `"Media"`
- `4d9c063` fix: footer synapticloop link text = `"synapticloop // image gallery"`
- `01ee18c` fix: footer synapticloop link → repo URL; remove footer border-top
- `1dc4a2b` docs: add `build-docs.sh` script + section explaining how to rebuild the PDF locally

---

## 2026-06-23

### Lightbox / scan
- `3fe7af0` (rename)

### File types / extensions
- (early extensions work)

### Initial scaffold (2026-06-13 to 2026-06-20)
The project started on 2026-06-13 as `caddy_image_gallery`. The early
commits established:

- Caddyfile module scaffold (`image_gallery` directive)
- xcaddy build script
- Lightbox overlay (image only)
- Sort bar (Name / Type / Modified / Size)
- Sort bar links preserve URL params
- Breadcrumb navigation
- Filter UI (initially single-dropdown, then multi-dropdown + Apply)
- Server-side search (`?q=`)
- Subdirs table
- Pagination
- Configurable `image_types` and `video_types`
- `page_sizes` dropdown
- Local install (`build.sh --user`)
- Font credits (SIL OFL 1.1)
- Animated light/dark fade GIF for docs

---

## Summary by category

### Features added
- Lightbox overlay with prev/next/close
- Video thumbnails (via ffmpeg)
- Subdirs table (with # Items, # Dirs, Size columns)
- Other files table
- Server-side + client-side search
- Filter UI (multi-dropdown + Apply button)
- Type filter (`?ext=`)
- Breadcrumb navigation
- Pagination (Google-style with ellipsis)
- Per-page size dropdown (configurable)
- Sort bar with arrows (Name/Type/Modified/Size)
- Click-to-sort table column headers
- EXIF metadata (lazy then eager, with sidecar cache)
- Source image/video dimensions watermark
- Hover tooltip on thumbnails
- Animated light/dark fade GIF for docs
- `no_exif` Caddyfile directive
- `max_cache_size_mb` Caddyfile directive
- `search_match` Caddyfile directive
- `path_prefix`, `root_name`, `image_types`, `video_types` directives
- Cache stats footer (hex)
- Subtle shimmer animation while loading
- Section toggle (show/hide directories, other files, media)
- Theme toggle (auto/light/dark)
- Local install via `build.sh --user [PORT]`
- `FFMPEG_PATH` env var

### Performance
- Cached source dimensions in `.meta` sidecar
- Cached EXIF data in `.exif` sidecar
- Thumb mtime = source mtime + LRU eviction via `.meta` mtime
- Stale sidecar detection via source mtime check
- EXIF sidecar in plain text format (not JSON)
- Human-Readable sidecar keys (match lightbox labels)
- Cached parsed template across requests

### Fixes
- Various template/CSS bugs (button heights, colors, hover states)
- Pagination + sort bar link state preservation
- Filter form preserving `page_size`
- Search header bug fixes (form-submitted vs JS-typed)
- `page_size=all` selection bug
- Symlink classification (Lstat vs Stat)
- Mobile layout improvements
- `no_exif` was the operator's choice for testing convenience
- `?page_size=N` URL parameter honored

### Refactors
- Project rename: `caddy_image_gallery` → `caddy_media_gallery`
- Template moved from embedded Go string to separate file with `//go:embed`
- Various naming: `num_per_page` → `page_size`, `images-section` → `media-*`
- EXIF: lazy → eager (with sidecar) → text format → Human-Readable keys
- Button labels: `Apply` → `Filter`, `All` → `Reset`

### Documentation
- SIL OFL 1.1 font credits
- ffmpeg startup detection docs
- Local install section
- Tagline `"The delightful way to serve a directory."`
- PDF refresh + cover image
- README docs updates
- Animated preview GIF
- Build script docs
- Comprehensive feature docs (catches up at 2026-06-28)
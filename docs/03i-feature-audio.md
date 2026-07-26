# Feature: audio (1.1.0+)

Per user request 2026-07-04 (audio-integration branch):
the gallery now supports audio-only files (mp3, flac, opus,
m4a, etc.) as a first-class media type, alongside images and
videos. Files matching the operator's `audio_types` directive
are classified as `KindAudio`, get an SVG speaker-icon tile
placeholder, a `<audio controls>` element in the lightbox, and
stream-level metadata (codec, sample rate, channels, channel
layout, duration, bitrate) extracted via `ffprobe` and cached
in `.ameta` sidecars.

Audio is **opt-in** since 1.1.0 — the default `audio_types`
list is empty, so existing 1.0.x configurations are unaffected
(operator opt-in is required).

## What's in 1.1.0

| | |
|---|---|
| **New Caddyfile directive** | `audio_types <list>` (e.g. `audio_types .mp3 .m4a .aac .flac .opus .wav .ogg .oga`). Same shape as `image_types` / `video_types`. Empty by default. |
| **New Caddyfile directive** | `no_audio_meta` (no arg → true, accepts "false"). Defaults to FALSE (audio metadata extraction on by default when `audio_types` is enabled). |
| **New FileKind** | `KindAudio` — files whose extension matches `audio_types` AND that don't match `video_types`. Per Q2 ("video wins"): files with video streams are `KindVideo`, files with only audio streams are `KindAudio`. |
| **New metadata struct** | `AudioMeta` with Tier-1 fields: `Codec`, `SampleRate`, `Channels`, `ChannelLayout`, `Duration`, `Bitrate`. NO ID3 / Vorbis / iTunes tag extraction (per Q5 — Tier 1 only). |
| **New sidecar format** | `.webp.ameta` — parallel to `.webp.vmeta` (video). Sidecar text format: `has=true\|false\n` followed by `Key=Value\n` lines. |
| **Filter UI** | New "Audio" dropdown between "Videos" and "Other" (per Q7). Hidden when `audio_types` is empty (no audio files match). |
| **Card tile placeholder** | Inline SVG speaker-icon (Material Design `volume_up` glyph, fill="currentColor" themed via `--accent` CSS variable). Warm-purple gradient background distinguishes audio cards from video cards at a glance. |
| **Lightbox audio player** | Native `<audio controls preload="metadata">` element. Same open-source path as the video lightbox (data-audio-* attributes, no JS plugin). |
| **Lightbox audio metadata panel** | Parallel to the video META panel. Six rows: codec, sample rate, channels, channel layout, duration, bitrate. Collapsible (localStorage state). |
| **i18n** | 5 new lang keys × 8 locales = 40 new translations: `filter_audio`, `meta_codec`, `meta_sample_rate`, `meta_channels`, `meta_channel_layout`. |
| **Operator UX** | Q3 fallback: if `audio_types` is set but `ffmpeg/ffprobe` isn't available, a startup stderr WARNING is logged ("audio files will be served without metadata enrichment"), and audio files still work — they get `KindAudio`, the audio filter matches, the SVG tile renders, the `<audio controls>` lightbox plays, but the metadata panel is empty. |

## Caddyfile usage

```caddyfile
media_gallery {
    path_prefix /images/
    root_name images

    # Enable audio support. Empty by default in 1.1.0.
    # Recommended starting list covers all browser-playable
    # audio formats. Excludes .webm (which is already a
    # video container; including it here would have no
    # effect since the Q2 "video wins" rule applies).
    audio_types .mp3 .m4a .aac .flac .opus .wav .ogg .oga
}
```

If you want to skip metadata extraction (e.g. for performance
on a large music library):

```caddyfile
media_gallery {
    audio_types .mp3 .flac .opus
    no_audio_meta    # disable AudioMeta extraction; the audio
                    # file still works (filter, tile, lightbox
                    # player) but the metadata panel is empty
}
```

## Class details

For each kind, the gallery renders different tile
placeholders + lightbox content. The visitor's experience:

| Kind | Tile placeholder | Lightbox content |
|---|---|---|
| Image | Real `<img>` thumbnail (WebP/PNG/JPEG) | Native `<img>` zoomed |
| Video | Video-frame thumbnail (extracted by ffmpeg) | `<video controls>` |
| **Audio (1.1.0+)** | **SVG speaker-icon (Material `volume_up`) on warm-purple gradient** | **`<audio controls>` + metadata panel** |
| Other | 📄 icon | Link to download (or whatever the browser does with the file) |

## Architecture: the Tier-1 audio metadata pipeline

Mirrors the existing EXIF + video-meta pipeline:

```
Operator Caddyfile:    audio_types .mp3 .flac .opus
                            ↓
Provision() (gallery.go): populates g.audioExtsMap, logs warning
                          if ffmpeg/ffprobe missing
                            ↓
Scanner.Scan() (scanner.go): Classify(name, imageExts, videoExts,
                                 audioExts) → KindImage/KindVideo/
                                 KindAudio/KindOther per Q2
                            ↓
Scanner.enrichParallel() (scanner.go): in background goroutine,
                                   if KindAudio && !NoAudioMeta:
                                   readAudioMetaCached(path, ...)
                            ↓
readAudioMetaCached (audio_meta.go): checks .ameta sidecar, on
                                    miss calls ffprobe (2 subprocess
                                    calls: stream info + format),
                                    writes sidecar
                            ↓
ffprobe parses: stream=codec_name/sample_rate/channels/
               channel_layout; format=duration/bit_rate
                            ↓
FileInfo.AudioMeta populated, FileView.AudioMeta + AudioMetaAttrs
                            ↓
Card rendering (render.go buildCardHTML):
   - <a class="card audio">
   - <div class="thumb thumb-audio"> with SVG icon
   - data-audio-* attributes for the lightbox
   - data-audio-duration pill (if Duration non-empty)
                            ↓
Lightbox (templates/gallery.tmpl JS):
   - detect .audio class → create <audio controls> element
   - read data-audio-* attrs → populate lb-audio-meta panel
```

## The "video wins" rule (Q2)

Per Q2: a file's Kind is decided by its dominant stream
content, not its container alone.

- `.ogg` file with Theora video stream + Vorbis audio stream
  → **KindVideo** (video stream present)
- `.ogg` file with only Vorbis audio
  → **KindAudio**
- `.mp3` file (always audio-only)
  → **KindAudio** (matches audio_types)
- `.mp4` file with H.264 video + AAC audio
  → **KindVideo** (matches video_types)

In 1.1.0 the classification is **extension-only** (at scan
time, Classify() looks at the extension). A future enhancement
could ffprobe every file to check stream presence, but that's
out of scope for 1.1.0 — see "Future enhancements" below.

If the operator's `video_types` and `audio_types` overlap
on an extension (e.g. both contain `.ogg` or `.mp4`), the
default `video_types` is preferred. The `video wins` rule
preserves backwards-compatibility: existing operators who
already had `.ogg` or `.mp4` in `video_types` will see
no behavior change.

## The "ffmpeg missing" fallback (Q3)

Per Q3: if the operator enables `audio_types` but
`ffmpeg/ffprobe` isn't installed, a single-line WARNING is
logged at Provision():

```
warning: caddy-media-gallery: audio_types was set ([.mp3 .m4a])
but ffmpeg/ffprobe is not installed; audio files will be served
without metadata enrichment (file playback in the browser still
works)
```

The audio file continues to work:

| | With ffmpeg | Without ffmpeg |
|---|---|---|
| File classified as KindAudio | ✅ | ✅ |
| Audio filter matches | ✅ | ✅ |
| SVG tile renders | ✅ | ✅ |
| Lightbox `<audio controls>` plays | ✅ | ✅ |
| Audio metadata panel populated | ✅ | ❌ (empty) |
| Duration pill on card | ✅ | ❌ (no pill) |

Only the metadata fields are empty. The audio file is still
classified, filtered, and playable. This matches the "video
wins" principle for 1.0.x where videos were still served even
when ffmpeg was missing (just with a black play-button instead
of a poster frame).

## Operator UI

The new "Audio" filter dropdown is positioned between
"Videos" and "Other" (per Q7):

```
Filter: [Images ▼] [Videos ▼] [Audio ▼] [Other ▼] [Filter] [Reset]
                1/3        1/1      0/0      0/0
```

When the operator hasn't set `audio_types`, the Audio dropdown
has 0 options and the template renders no pill at all. Operators
who visit a fresh installation see only Images / Videos / Other
(unchanged from 1.0.x); the Audio pill appears only when
there's an audio file to filter.

When audio_types IS set, the Audio dropdown lists each audio
extension present in the current directory with a checkbox
(matching the existing Images / Videos / Other UX). Selecting
options filters the gallery by those extensions. The Filter
button + Reset button work as before.

## Lang keys (5 new × 8 locales = 40 translations)

| Key | en | de | es | fr | ja | ko | zh | pt |
|---|---|---|---|---|---|---|---|---|
| `filter_audio` | Audio | Audio | Audio | Audio | オーディオ | 오디오 | 音频 | Áudio |
| `meta_codec` | Codec | Codec | Códec | Codec | コーデック | 코덱 | 编解码器 | Codec |
| `meta_sample_rate` | Sample rate | Abtastrate | Frecuencia de muestreo | Taux d'échantillonnage | サンプルレート | 샘플 레이트 | 采样率 | Taxa de amostragem |
| `meta_channels` | Channels | Kanäle | Canales | Canaux | チャンネル数 | 채널 수 | 声道数 | Canais |
| `meta_channel_layout` | Channel layout | Kanal-Layout | Disposición de canales | Disposition des canaux | チャンネルレイアウト | 채널 배치 | 声道布局 | Disposição dos canais |

(Reuses the existing `meta_duration`, `meta_bitrate`, `meta_label`
keys for the duration / bitrate rows and the panel header —
the audio metadata panel structure is parallel to the video
metadata panel.)

## Scanner cache key

The `extSetsKey()` function in `scancache.go` (which produces
the cache key for the per-directory scan cache) now includes
the audio ext set + the `no_audio_meta` flag:

```
"i:jpg,jpeg,png,gif,webp|v:mp4,webm,m4v,mov,mkv,avi,ogv,ogg|a:mp3,flac,opus|e:0|m:0|M:0"
```

This means toggling `audio_types` or `no_audio_meta` in the
Caddyfile invalidates the scan cache (so visitors don't see
stale `KindAudio=0` or stale `AudioMeta` fields after the
operator changes the config).

## Tests

The audio metadata pipeline is covered by `audio_meta_test.go`:

- `TestFormatSampleRate` — covers 48000, 44100, 22050, 11025, 96000
  plus edge cases (empty, "N/A", garbage). Verifies the 3-decimal
  format ("11.025 kHz" not "11.03 kHz") and the trimming of
  trailing zeros.
- `TestFormatAudioDurationBitrate` — covers the shared
  formatters. 83s → "1:23", 5025s → "1:23:45", 128_000 → "128 kbps",
  5_200_000 → "5.2 Mbps".
- `TestAudioMetaHasAny` — verifies the empty-AudioMeta `HasAny()`
  returns false; populated-AudioMeta returns true.
- `TestAudioMetaSidecarRoundTrip` — write + parse round-trip
  preserves all six fields.
- `TestAudioMetaSidecarEmpty` — empty AudioMeta serializes to
  `"has=false\n"`, parses to nil.
- `TestAudioMetaSidecarMalformed` — `nil`, `garbage`,
  `has=true\n`, `has=foo\n` all return nil.
- `TestParseAudioStreamEntries` — verifies parsing of mp3
  44100Hz, flac 96000Hz, and empty input.
- `TestParseAudioFormatEntries` — verifies the FORMAT block
  parser.
- `TestReadAudioMetaWithFfmpeg` — full live test against
  ffmpeg. Skipped if ffmpeg isn't installed.

Plus a quick Card render verification via the live server
(after the rebuild) confirms the SVG placeholder tile +
`data-audio-*` attributes + audio filter pill all appear.

## Future enhancements (out of scope for 1.1.0)

These could be added in 1.1.x or 1.2.x:

1. **Stream-aware classification**: ffprobe every file at scan
   time to check whether the dominant stream is video or audio.
   Would let `.ogg` files with Theora go to KindVideo AND
   `.ogg` files with only Vorbis go to KindAudio, even when both
   extensions are in `audio_types`. 1.1.0 only does extension-based
   classification (faster; no extra ffprobe at scan time).
2. **Tier 2 metadata**: extract ID3 / Vorbis / iTunes tags
   (title, artist, album, year, track, etc.). Per Q5, Tier 1 only
   in 1.1.0 — the data isn't on the card or in the lightbox
   yet. The sidecar format is extensible (just add `Title=`,
   `Artist=`, etc. lines).
3. **Cover art extraction**: pull embedded cover art (MP3 APIC
   frames, FLAC pictures) as the audio card's poster / lightbox
   thumbnail. Would let an audio file with cover art show
   the album cover instead of the generic speaker icon. Tier 3.
4. **Waveform / playback-position indicator**: a CSS-only
   progress bar that mirrors the `<audio>` element's current
   time. Mostly cosmetic.

## See also

- `README.md` — operator-facing overview
- `docs/01-configuration.md` — Caddyfile directive reference
- `docs/02-configuration-reference.md` — one-page index
- `docs/03e-feature-lightbox.md` — lightbox architecture
  (the audio lightbox is a parallel path)
- `docs/03h-feature-i18n.md` — internationalisation
  infrastructure (the audio translation keys follow the
  same pattern)
- `audio_meta.go` — Tier-1 metadata pipeline
  (`AudioMeta` struct, ffprobe integration, .ameta sidecar)
- `video_meta.go` — parallel video pipeline (the audio
  pipeline mirrors this)
- `CHANGELOG.md` — `## 1.1.0` section for the actual
  release notes
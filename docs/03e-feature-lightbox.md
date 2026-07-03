# Templates: lightbox controls

## Lightbox controls

The lightbox overlay has 4 buttons + 2 text labels:

| Element | Position | Behavior |
|---|---|---|
| `.lb-close` (×) | Top-right (inside `.lb-controls` pill) | Closes the lightbox; Esc key |
| `.lb-open` (↗) | Top-right (inside `.lb-controls` pill, left of close) | Opens the current image/video in a new tab |
| `.lb-prev` (‹) | Full-height hit area on the **left** side (120px wide) | Previous image; Left-arrow key |
| `.lb-next` (›) | Full-height hit area on the **right** side (120px wide) | Next image; Right-arrow key |
| `.lb-counter` | Bottom-left (or bottom-center on mobile) | "N / total" text |
| `.lb-caption` | Bottom-center | The current file's name |

**The `.lb-controls` rounded pill (Phase 48):** the open (↗)
and close (×) buttons are wrapped in a single flex container
`.lb-controls` so they appear as a single rounded pill at the
top-right of the lightbox. The container has:
- Position: `top: 1rem, right: 1.5rem`
- Background: `rgba(255, 255, 255, 0.92)` (light pill on the
  dark lightbox)
- Border: `2px solid #000`
- Border-radius: `10px` (the "rounded box")
- Padding: `4px` around the buttons
- Display: `flex` (lays out open + close side-by-side, perfectly
  aligned vertically and horizontally)

The two buttons inside have:
- `position: static` (flex lays them out, not absolute)
- Transparent background (the container provides it)
- No individual border
- 28×28 px
- Dark icon color (`#1a1a26`)
- `border-radius: 6px` on each button (rounded corners within
  the pill)
- Hover: subtle dark background tint (`rgba(0, 0, 0, 0.08)`)

**Why a pill instead of separate buttons:** a single rounded
container gives the two related actions (close + open) a visual
unity — they're both "exit the lightbox" actions (close or
view-on-its-own), and grouping them makes that clearer.

**The prev/next buttons are NOT in the pill** — they're at the
left/right edges of the lightbox. These are navigation actions
(different from exit actions), so they get their own positioning.

**Prev/next hit areas (Phase 65):** the prev/next buttons are
**full-window-height × 120px wide** hit areas positioned at
`left: 0` / `right: 0`. The arrow icon is flex-centered inside
the hit area. At rest, the hit area is **transparent** (no
visible button) — only a hover reveals the target.

- **Hover background — theme-aware:**
  - Dark mode (default; the page bg is dark): the hover bg is
    `rgba(255, 255, 255, 0.08)` — a subtle whiter tint over the
    dark lightbox. The user sees a soft "highlight" where they're
    pointing.
  - Light mode (page bg is light): the hover bg is
    `rgba(0, 0, 0, 0.06)` — a subtle darker tint. The lightbox
    itself is still theme-independent (always dark), but the
    hover tint adapts so it works for visitors on light pages.

**Why a full-height hit area:**
- On touch devices (mobile, tablets), the user doesn't have a
  precise cursor — a small button in the middle of the screen
  is hard to hit. A full-height strip on each side gives a
  large, easy target.
- On desktop, the same hit area means the user can click
  anywhere in the left or right strip — no need to aim.

**Why 120px wide:** enough to be a comfortable target on touch
screens (~7mm at typical DPI), small enough to leave the center
~60% of the screen clear for the image/video content.

**Why transparent at rest:** the lightbox is about the content.
A visible button on each side would distract from the image.
The hover reveals the hit area, so the user sees it when they're
navigating, not when they're viewing.

**JS — `stopPropagation` (Phase 65 fix):** the new hit areas sit
on top of the media (z-index: 1). The media element has its own
click handler that advances to the next image (for `<img>`).
Without `stopPropagation` on the prev/next click handlers, a
single click in the prev/next area would advance TWICE — once
from the button, once from the bubbling media click. The fix:
both prev/next handlers call `e.stopPropagation()` before
calling `show(idx±1)`.

**Why an alpha-blended hover (rgba) instead of a solid color:**
the lightbox shows whatever media is loaded. A solid bg color
would clash with some images (e.g., a green hover on a photo
of a red rose). An alpha-blended fill mixes with whatever's
behind, giving a consistent "tint" effect that works on any
media.

**Why a dark border:** like the tile `.open-btn`, the pill has
a black 2px border so it stands out clearly against the dark
lightbox background. The light pill on a dark bg with a dark
border is a strong visual "this is a control" signal.

**Keyboard navigation:**
- `Escape` → close
- `Left arrow` → previous image
- `Right arrow` → next image
- (Clicking on the image itself also goes to next, like
  carousel UIs)

The `data-theme` attribute on `<html>` is NOT read by the
lightbox CSS — the lightbox is intentionally theme-independent
(dark always, with light controls). This is the same design
choice as the lightbox bg and counter/caption: focus
on the content, regardless of page theme.

**Video poster (Phase 63):** when a video tile has a generated
thumbnail (Phase 62's ffmpeg pipeline), the lightbox video
element sets its HTML5 `poster` attribute to the thumb URL
(extracted from the same `<img class="thumb-img">` element
on the tile). The browser shows the poster image immediately
when the video opens in the lightbox — the user sees the
video's first frame as a still image, then on click the video
swaps to playback. This is the same mechanism YouTube uses
to show a thumbnail before a video plays.

If `no_video_thumbs` is set OR ffmpeg is missing, the tile
has no `<img class="thumb-img">` so the JS can't find a
poster URL — the `poster` attribute is simply not set, and
the browser shows its default (black frame, or the first
decoded frame if `preload="metadata"` is enabled).

The poster URL points at the same cached WebP that's used
for the tile thumbnail, so no extra server work or storage
is required.

## Mobile: video play button fix (Phase 61)

On mobile devices, clicking the play button on a video in the
lightbox used to advance to the next media file instead of
starting playback. Root cause: the media click handler always ran
`show(idx + 1)` regardless of media type. On mobile, tapping the
video's native play button fires a click event on the `<video>`
element, and the handler advanced to the next file BEFORE the
video could play. Fix (Phase 61): check
`currentEl.tagName === 'VIDEO'` in the handler and bail out so
the browser's native click handling (play/pause) takes over.

For images, click-to-advance still works (unchanged). For
videos, click is no longer hijacked — the user can tap the play
button on mobile to start playback, or click elsewhere on the
video (e.g. the time bar / volume) for the native controls to
handle. Navigation is via the prev/next buttons or arrow keys.

## Open-in-new-tab button (Phase 47+52)

The tile-level open-in-new-tab button (the small ↗ in the
top-right of each tile) is documented separately in its own
section above. Recent refinements:

- **Phase 47** — added the dark border (`border: 2px solid #000`)
  so the button stands out more. The light translucent bg
  (`rgba(255,255,255,0.85)`) stays light over both light and
  dark page bgs, so a dark border is always visible.
- **Phase 52** — arrow color fixed at `#111111` (was
  `var(--fg)`). The user explicitly wanted `--fg` UNCHANGED
  (still `#111111` light / `#e5e5e5` dark) and used by other
  elements. The open-btn arrow stays dark in both modes because
  the button's bg is always light.

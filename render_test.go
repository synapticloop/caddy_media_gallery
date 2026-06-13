package gallery

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestRenderPage_ContainsImagesAndFilenames(t *testing.T) {
	files := []FileInfo{
		{Name: "alpha.jpg", ModTime: time.Now().UnixNano(), Size: 12345, Kind: KindImage},
		{Name: "beta.png", ModTime: time.Now().UnixNano(), Size: 67890, Kind: KindImage},
		{Name: "gamma.mp4", ModTime: time.Now().UnixNano(), Size: 999999, Kind: KindVideo},
		{Name: "readme.txt", ModTime: time.Now().UnixNano(), Size: 100, Kind: KindOther},
	}
	html, err := RenderPage("Test Gallery", "./", "./_thumbs/", "", files, nil)
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	for _, want := range []string{
		"Test Gallery",
		"alpha.jpg", "beta.png", "gamma.mp4", "readme.txt",
		"Other files",          // "Other files" section header must appear
		"<img",                 // image tags must be emitted
		"loading=\"lazy\"",     // lazy loading
		"./_thumbs/alpha.webp", // thumb URL: basename + .webp
		"./_thumbs/beta.webp",
		"gallery-lightbox",               // lightbox overlay element id
		"lb-prev", "lb-next", "lb-close", // lightbox controls
		"ArrowLeft", "ArrowRight", "Escape", // lightbox keybindings
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rendered HTML missing %q", want)
		}
	}
}

func TestRenderPage_NoOtherFilesSectionWhenEmpty(t *testing.T) {
	files := []FileInfo{
		{Name: "only.jpg", ModTime: time.Now().UnixNano(), Kind: KindImage},
	}
	html, err := RenderPage("x", "./", "./_thumbs/", "", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "Other files") {
		t.Error("expected no 'Other files' section when there are no non-image/non-video files")
	}
}

func TestRenderPage_HTMLIsValidish(t *testing.T) {
	files := []FileInfo{
		{Name: "a.jpg", ModTime: time.Now().UnixNano(), Kind: KindImage},
	}
	html, err := RenderPage("t", "./", "./_thumbs/", "", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	checks := []struct{ open, close string }{
		{"<html", "</html>"},
		{"<head>", "</head>"},
		{"<body>", "</body>"},
		{"<div", "</div>"},
	}
	for _, c := range checks {
		if !strings.Contains(html, c.open) || !strings.Contains(html, c.close) {
			t.Errorf("missing %q or %q in HTML", c.open, c.close)
		}
	}
}

func TestRenderPage_DirectoriesAlwaysRendered(t *testing.T) {
	// Directories should appear at the top, regardless of pagination
	// or sort. A 200-image page with 3 dirs and 197 images should
	// still show all 3 dirs in full.
	var files []FileInfo
	for i := 0; i < 3; i++ {
		files = append(files, FileInfo{Name: dirName(i), ModTime: 0, Kind: KindDir})
	}
	for i := 0; i < 200; i++ {
		files = append(files, FileInfo{Name: imageName(i), ModTime: int64(i), Size: 1024, Kind: KindImage})
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"dir-0/", "dir-1/", "dir-2/"} {
		if !strings.Contains(html, d) {
			t.Errorf("expected directory %q in HTML", d)
		}
	}
	// The pagination block should be present (200 images, 50/page = 4 pages)
	if !strings.Contains(html, "Page 1 of 4") {
		t.Error("expected pagination to show 4 pages for 200 images")
	}
}

func TestRenderPage_PaginationLinksPresent(t *testing.T) {
	// 200 images, 50 per page = 4 pages
	var files []FileInfo
	for i := 0; i < 200; i++ {
		files = append(files, FileInfo{Name: imageName(i), ModTime: int64(i), Size: 1024, Kind: KindImage})
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	// First page: should have "Next" but not "← Prev" as a link
	if !strings.Contains(html, `href="?sort=mtime&order=desc&page=2"`) {
		t.Error("expected Next link to page 2")
	}
	// Test page 2
	q := url.Values{"page": {"2"}}
	html2, err := RenderPage("test", "./", "./_thumbs/", "", files, q)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html2, "Page 2 of 4") {
		t.Error("expected 'Page 2 of 4' on page 2")
	}
	if !strings.Contains(html2, `href="?sort=mtime&order=desc&page=1"`) {
		t.Error("expected Prev link to page 1 on page 2")
	}
	if !strings.Contains(html2, `href="?sort=mtime&order=desc&page=3"`) {
		t.Error("expected Next link to page 3 on page 2")
	}
}

func TestRenderPage_SortUITogglesDirection(t *testing.T) {
	files := []FileInfo{
		{Name: "a.jpg", ModTime: time.Now().UnixNano(), Size: 1024, Kind: KindImage},
	}
	// Default (no sort param): the Name button should be inactive.
	// Clicking it should go to ?sort=name&order=asc.
	// (Go's html/template leaves & unescaped in href attributes —
	// they're valid HTML — so we check for & not &amp;.)
	html, _ := RenderPage("test", "./", "./_thumbs/", "", files, nil)
	if !strings.Contains(html, `href="?sort=name&order=asc"`) {
		t.Error("expected default Name link to be asc (clicking activates sort)")
	}

	// Now activate by name asc. The link should toggle to desc.
	q := url.Values{"sort": {"name"}, "order": {"asc"}}
	html, _ = RenderPage("test", "./", "./_thumbs/", "", files, q)
	if !strings.Contains(html, `class="sort-btn active"`) {
		t.Error("expected the active sort button to have the 'active' class")
	}
	if !strings.Contains(html, `href="?sort=name&order=desc"`) {
		t.Error("expected active Name link to toggle to desc")
	}
	// The active button should also display an arrow.
	if !strings.Contains(html, `class="arrow"> ↑</span>`) {
		t.Error("expected active sort button to show ↑ arrow for asc")
	}
}

func TestRenderPage_TileMetadata(t *testing.T) {
	// Each IMAGE tile should have: name, date (YYYY-MM-DD in UTC),
	// human-readable size, and a filetype chip (uppercase, no dot).
	// Non-image "Other files" chips are a separate concern and
	// have a different markup (just name + type).
	now := time.Date(2026, 6, 12, 14, 30, 0, 0, time.UTC)
	files := []FileInfo{
		{Name: "photo.jpg", ModTime: now.UnixNano(), Size: 234567, Kind: KindImage},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `class="tile-name">photo.jpg`) {
		t.Error("expected tile to show filename as tile-name")
	}
	if !strings.Contains(html, "2026-06-12") {
		t.Error("expected ISO-formatted date on tile (UTC-normalised)")
	}
	if !strings.Contains(html, "229.1 KB") {
		t.Error("expected human-readable size on tile (229.1 KB for 234567 bytes)")
	}
	if !strings.Contains(html, `class="filetype-chip">JPG`) {
		t.Error("expected JPG filetype chip on image tile")
	}
}

func TestRenderPage_EmptyDirShowsEmptyMessage(t *testing.T) {
	html, err := RenderPage("empty", "./", "./_thumbs/", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "No images in this directory.") {
		t.Error("expected 'No images' message for empty directory")
	}
}

func TestRenderPage_OtherFilesHorizontalStrip(t *testing.T) {
	// Verify that non-image files (HTML, txt) appear in the
	// "Other files" section as chips, NOT in the image grid.
	// Videos (per the user's spec) go in the IMAGE grid, not in
	// the "Other files" strip.
	files := []FileInfo{
		{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "notes.txt", ModTime: 2, Size: 50, Kind: KindOther},
		{Name: "clip.mp4", ModTime: 3, Size: 9999, Kind: KindVideo},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The image section header should appear exactly once.
	if c := strings.Count(html, ">Images<"); c != 1 {
		t.Errorf("expected exactly one 'Images' section, got %d", c)
	}
	// The "Other files" section should appear exactly once.
	if c := strings.Count(html, "Other files"); c != 1 {
		t.Errorf("expected exactly one 'Other files' section, got %d", c)
	}
	// notes.txt should be in the "Other files" section.
	// clip.mp4 should be in the image grid section (with a play-button).
	othersIdx := strings.Index(html, "Other files")
	imagesIdx := strings.Index(html, ">Images<")
	if othersIdx < 0 || imagesIdx < 0 {
		t.Fatal("could not find both 'Other files' and 'Images' sections")
	}
	othersSection := html[othersIdx:imagesIdx]
	imagesSection := html[imagesIdx:]
	if !strings.Contains(othersSection, "notes.txt") {
		t.Error("notes.txt should be in the 'Other files' section")
	}
	if strings.Contains(othersSection, "clip.mp4") {
		t.Error("clip.mp4 should NOT be in the 'Other files' section — it belongs in the image grid")
	}
	if !strings.Contains(imagesSection, "clip.mp4") {
		t.Error("clip.mp4 should be in the image grid section")
	}
	if !strings.Contains(imagesSection, "a.jpg") {
		t.Error("a.jpg should be in the image grid section")
	}
	// Video tile should use the play-overlay, not an <img>.
	if !strings.Contains(imagesSection, "play-overlay") {
		t.Error("expected video tile to use play-overlay (not <img>)")
	}
	// Video tile should have a .video class.
	if !strings.Contains(imagesSection, `class="card video"`) {
		t.Error("expected video tile to have class 'card video'")
	}
	// Image tile should NOT have a .video class.
	if strings.Contains(imagesSection, `class="card video"`) && !strings.Contains(imagesSection, "clip.mp4") {
		t.Error("image tile should not have 'card video' class")
	}
}

func TestRenderPage_UpEntryInSubdir(t *testing.T) {
	// When viewing a subdirectory, an ".." entry should be prepended
	// to the directories list. The Href is "../" (one level up
	// relative to the current page).
	files := []FileInfo{
		{Name: "nested1", Kind: KindDir},
		{Name: "nested2", Kind: KindDir},
		{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	// Viewing a subdir: relPath = "subdir"
	html, err := RenderPage("subdir", "./", "./_thumbs/", "subdir", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The ".." entry should be the first dir chip.
	othersIdx := strings.Index(html, "Other files")
	if othersIdx < 0 {
		othersIdx = len(html)
	}
	dirsSection := html[:othersIdx]
	upIdx := strings.Index(dirsSection, `>../<`)
	if upIdx < 0 {
		t.Fatal("expected '..' entry in the directories section for a subdir view")
	}
	// Href should be "../"
	if !strings.Contains(dirsSection, `href="../"`) {
		t.Error("expected '..' entry to link to '../'")
	}
	// The up entry should be the FIRST dir chip (before the real dirs).
	// Find positions of the first "..</a>" and the first "nested1</a>"
	upEnd := strings.Index(dirsSection, "</a>") // first </a> closes the up entry
	firstNestedPos := strings.Index(dirsSection, "nested1</a>")
	if firstNestedPos > 0 && upEnd > 0 && upEnd > firstNestedPos {
		t.Error("expected '..' entry to appear before real directories")
	}
}

func TestRenderPage_NoUpEntryAtRoot(t *testing.T) {
	// At the gallery root, no ".." entry should appear.
	files := []FileInfo{
		{Name: "nested1", Kind: KindDir},
		{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	html, err := RenderPage("root", "./", "./_thumbs/", "", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	// No "..</a>" in the dirs section.
	othersIdx := strings.Index(html, "Other files")
	if othersIdx < 0 {
		othersIdx = len(html)
	}
	dirsSection := html[:othersIdx]
	if strings.Contains(dirsSection, `>../<`) {
		t.Error("did not expect '..' entry at the gallery root")
	}
}

func TestRenderPage_SortIndicatorInHeader(t *testing.T) {
	files := []FileInfo{
		{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	// Default sort: should show "Sort: Modified ↓" as a span (not a link).
	html, err := RenderPage("test", "./", "./_thumbs/", "", files, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `class="sort-indicator"`) {
		t.Fatal("expected sort indicator in header")
	}
	if !strings.Contains(html, "Sort: Modified") {
		t.Error("expected default sort indicator to say 'Sort: Modified'")
	}
	// The default indicator is a span (not clickable).
	if !strings.Contains(html, `<span class="sort-indicator"`) {
		t.Error("expected default sort indicator to be a <span> (not clickable)")
	}

	// Custom sort: should show "Sort: Name ↑" as a link to clear.
	q := url.Values{"sort": {"name"}, "order": {"asc"}}
	html, _ = RenderPage("test", "./", "./_thumbs/", "", files, q)
	if !strings.Contains(html, `class="sort-indicator"`) {
		t.Fatal("expected sort indicator in header (custom sort)")
	}
	if !strings.Contains(html, "Sort: Name") {
		t.Error("expected custom sort indicator to say 'Sort: Name'")
	}
	if !strings.Contains(html, `href="?"`) {
		t.Error("expected custom sort indicator to be a link to reset (href=?)")
	}
}

// dirName returns a deterministic directory name for tests.
func dirName(i int) string { return "dir-" + intStr(i) }

// imageName returns a deterministic image name for tests.
func imageName(i int) string { return "img-" + intStr(i) + ".jpg" }

// intStr returns the int as a string with no leading zeros.
func intStr(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		buf[n] = '-'
	}
	return string(buf[n:])
}

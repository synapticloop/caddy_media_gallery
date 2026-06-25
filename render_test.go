package gallery

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMain sets GALLERY_TEMPLATES_DIR to a non-existent temp
// dir for the entire test process. Without this, any RenderPage
// call would pick up the real /etc/caddy/gallery-templates/gallery.tmpl
// if it happens to exist on the test host (e.g. from a previous
// build), which would diverge from the bundled template the tests
// are written against. By isolating tests to a temp dir, the
// loadTemplate() fallback to the bundled galleryTemplate constant
// is what gets used.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "caddy-media-gallery-test-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	os.Setenv("GALLERY_TEMPLATES_DIR", tmp)
	os.Exit(m.Run())
}

func TestRenderPage_ContainsImagesAndFilenames(t *testing.T) {
	files := []FileInfo{
		{Name: "alpha.jpg", ModTime: time.Now().UnixNano(), Size: 12345, Kind: KindImage},
		{Name: "beta.png", ModTime: time.Now().UnixNano(), Size: 67890, Kind: KindImage},
		{Name: "gamma.mp4", ModTime: time.Now().UnixNano(), Size: 999999, Kind: KindVideo},
		{Name: "readme.txt", ModTime: time.Now().UnixNano(), Size: 100, Kind: KindOther},
	}
	html, err := RenderPage("Test Gallery", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
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
	html, err := RenderPage("x", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
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
	html, err := RenderPage("t", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// First page: should have "Next" but not "← Prev" as a link
	if !strings.Contains(html, `href="?sort=mtime&order=desc&page=2"`) {
		t.Error("expected Next link to page 2")
	}
	// Test page 2
	q := url.Values{"page": {"2"}}
	html2, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, q, nil, nil)
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

// TestRenderPage_PerPageTextInHeader verifies that the header
// meta line shows "N per page" where N is the configured page
// size. Per user request 2026-06-17: "add in number of results
// per page after the text '42 images · 2 other files · 21
// directories'".
func TestRenderPage_PerPageTextInHeader(t *testing.T) {
	// 7 images, pageSize=10 → "10 per page" should appear in
	// the header meta line, after the "N images" count.
	files := []FileInfo{
		{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "b.jpg", ModTime: 2, Size: 100, Kind: KindImage},
		{Name: "c.jpg", ModTime: 3, Size: 100, Kind: KindImage},
		{Name: "d.jpg", ModTime: 4, Size: 100, Kind: KindImage},
		{Name: "e.jpg", ModTime: 5, Size: 100, Kind: KindImage},
		{Name: "f.jpg", ModTime: 6, Size: 100, Kind: KindImage},
		{Name: "g.jpg", ModTime: 7, Size: 100, Kind: KindImage},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 10, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Find the header meta div (class is "meta", not "meta-counts")
	metaStart := strings.Index(html, `class="meta"`)
	if metaStart < 0 {
		t.Fatal("expected meta-counts div in the header")
	}
	metaEnd := strings.Index(html[metaStart:], `</div>`)
	if metaEnd < 0 {
		t.Fatal("could not find end of meta-counts div")
	}
	metaBlock := html[metaStart : metaStart+metaEnd]
	// Should contain "7 images"
	if !strings.Contains(metaBlock, "7 images") {
		t.Errorf("expected '7 images' in the header meta block, got: %q", metaBlock)
	}
	// Should contain "10 per page" (the pageSize)
	if !strings.Contains(metaBlock, "10 per page") {
		t.Errorf("expected '10 per page' in the header meta block, got: %q", metaBlock)
	}
	// "10 per page" should come AFTER "7 images" in the meta block
	imagesIdx := strings.Index(metaBlock, "7 images")
	perPageIdx := strings.Index(metaBlock, "10 per page")
	if imagesIdx < 0 || perPageIdx < 0 || perPageIdx <= imagesIdx {
		t.Errorf("expected '10 per page' to come AFTER '7 images' in the header, got: %q", metaBlock)
	}
	// Should also work with a non-default pageSize (e.g. 25)
	html25, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 25, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html25, "25 per page") {
		t.Errorf("expected '25 per page' with pageSize=25, got: %q", html25)
	}
}

// TestRenderPage_HeaderShowsPageCount verifies that the
// header meta line shows the total page count after the
// "N per page" indicator when there is more than one page.
// Per user request 2026-06-17: "add the number of pages after
// the 50 per page".
func TestRenderPage_HeaderShowsPageCount(t *testing.T) {
	// 3 images at pageSize=10 -> ceil(3/10) = 1 page. No page
	// count shown (only when TotalPages > 1).
	files := []FileInfo{
		{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "b.jpg", ModTime: 2, Size: 100, Kind: KindImage},
		{Name: "c.jpg", ModTime: 3, Size: 100, Kind: KindImage},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 10, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	metaStart := strings.Index(html, `class="meta"`)
	metaEnd := strings.Index(html[metaStart:], `</div>`)
	metaBlock := html[metaStart : metaStart+metaEnd]
	if strings.Contains(metaBlock, "pages") {
		t.Errorf("expected NO 'pages' indicator when TotalPages=1, got: %q", metaBlock)
	}

	// 200 images at pageSize=10 -> 20 pages. Should show
	// "Page 1 of 20" (and NOT the old "N pages" indicator,
	// which was removed in Phase 37 per user request).
	files2 := make([]FileInfo, 200)
	for i := 0; i < 200; i++ {
		files2[i] = FileInfo{Name: imageName(i), ModTime: int64(i), Size: 1024, Kind: KindImage}
	}
	html2, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 10, files2, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	metaStart2 := strings.Index(html2, `class="meta"`)
	metaEnd2 := strings.Index(html2[metaStart2:], `</div>`)
	metaBlock2 := html2[metaStart2 : metaStart2+metaEnd2]
	if !strings.Contains(metaBlock2, "10 per page") {
		t.Errorf("expected '10 per page' in header, got: %q", metaBlock2)
	}
	if !strings.Contains(metaBlock2, "Page 1 of 20") {
		t.Errorf("expected 'Page 1 of 20' in header, got: %q", metaBlock2)
	}
	if strings.Contains(metaBlock2, "20 pages") {
		t.Errorf("expected NO '20 pages' indicator (removed in Phase 37), got: %q", metaBlock2)
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
	html, _ := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
	if !strings.Contains(html, `href="?sort=name&order=asc"`) {
		t.Error("expected default Name link to be asc (clicking activates sort)")
	}

	// Now activate by name asc. The link should toggle to desc.
	q := url.Values{"sort": {"name"}, "order": {"asc"}}
	html, _ = RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, q, nil, nil)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
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
	// Layout: date and size are stacked in a .tile-meta-info
	// wrapper, with the filetype chip OUTSIDE that wrapper.
	// Verify the stacking by checking that the order in the HTML
	// is: tile-meta opens, tile-meta-info opens, date, size,
	// tile-meta-info closes, filetype-chip, tile-meta closes.
	tileMetaInfoStart := strings.Index(html, `class="tile-meta-info"`)
	if tileMetaInfoStart < 0 {
		t.Fatal("expected a .tile-meta-info wrapper around date+size")
	}
	// Inside the wrapper: date should appear before size.
	wrapperEnd := strings.Index(html[tileMetaInfoStart:], `</div>`)
	wrapper := html[tileMetaInfoStart : tileMetaInfoStart+wrapperEnd]
	dateIdx := strings.Index(wrapper, `class="date"`)
	sizeIdx := strings.Index(wrapper, `class="size"`)
	if dateIdx < 0 || sizeIdx < 0 {
		t.Errorf("date and size should both be inside .tile-meta-info; got date=%d size=%d", dateIdx, sizeIdx)
	}
	if dateIdx > sizeIdx {
		t.Error("expected date to appear BEFORE size in the HTML (size under date)")
	}
}

func TestRenderPage_EmptyDirShowsEmptyMessage(t *testing.T) {
	html, err := RenderPage("empty", "./", "./_thumbs/", "", "", false, false, 0, nil, nil, nil, nil)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The image section header should appear exactly once.
	if c := strings.Count(html, ">Media ("); c != 1 {
		t.Errorf("expected exactly one 'Images' section, got %d", c)
	}
	// The "Other files" section should appear exactly once.
	if c := strings.Count(html, "Other files"); c != 1 {
		t.Errorf("expected exactly one 'Other files' section, got %d", c)
	}
	// notes.txt should be in the "Other files" section.
	// clip.mp4 should be in the image grid section (with a play-button).
	othersIdx := strings.Index(html, "Other files")
	imagesIdx := strings.Index(html, ">Media (")
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

// TestRenderPage_OtherFilesAsTable verifies the Phase 69
// change: other files are now rendered as a full-width
// table (not a chip row). The table has columns Name, Type,
// Size, Date (Size is included for files because it's
// meaningful; directories omit Size in the dirs-table).
func TestRenderPage_OtherFilesAsTable(t *testing.T) {
	files := []FileInfo{
		{Name: "readme.txt", ModTime: 100, Size: 1024, Kind: KindOther},
		{Name: "config.json", ModTime: 200, Size: 2048, Kind: KindOther},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The new structure: a <table class="files-table others-table">.
	if !strings.Contains(html, `<table class="files-table others-table">`) {
		t.Error(`expected <table class="files-table others-table"> in the rendered page`)
	}
	// Old structure should be GONE: no <div class="chip-row">
	// for the others section (was used in Phase 24-68).
	// We grep for the chip-row class — there's a .up-chip-row
	// that still uses a similar name, but that's a div, not
	// a "chip-row" exactly.
	othersStart := strings.Index(html, "Other files")
	if othersStart < 0 {
		t.Fatal("could not find 'Other files' section")
	}
	othersEnd := len(html)
	imgStart := strings.Index(html, ">Media (")
	if imgStart > 0 {
		othersEnd = imgStart
	}
	othersSection := html[othersStart:othersEnd]
	if strings.Contains(othersSection, `class="chip-row"`) {
		t.Error(`expected NO <div class="chip-row"> in the others section (replaced by table in Phase 69)`)
	}
	// Verify both files appear as table rows.
	rowCount := strings.Count(othersSection, "<tr>")
	if rowCount < 2 {
		t.Errorf("expected at least 2 <tr> rows in others section (one per file), got %d", rowCount)
	}
	// Verify Size column is present (only in others table, not dirs).
	if !strings.Contains(html, `class="col-size"`) {
		t.Error("expected col-size column in rendered page (others table)")
	}
	// Verify each file's name appears as a link in the Name cell.
	if !strings.Contains(html, "readme.txt") {
		t.Error("expected 'readme.txt' in the rendered page")
	}
	if !strings.Contains(html, "config.json") {
		t.Error("expected 'config.json' in the rendered page")
	}
}

func TestRenderPage_UpEntryInSubdir(t *testing.T) {
	// When viewing a subdirectory, an "Up" entry is rendered on
	// its OWN LINE (in a separate <div class="up-chip-row">)
	// and the subdirs are rendered in a SEPARATE <div
	// class="dirs-row"> with NO gap between chips. Per the
	// user's 2026-06-17 spec: "the up directory chip should
	// always be first and on its own line. remove the spacing
	// for the rest of the directories".
	// When viewing a subdirectory, an "Up" entry is rendered as
	// the first row of the dirs table (Phase 72: moved from a
	// separate up-chip-row above the table to a <tr class="up-row">
	// inside the table's <tbody>). The subdirs are then rendered
	// as separate <tr> rows. Per the user's 2026-06-17 spec: "the
	// up directory chip should always be first and on its own
	// line. remove the spacing for the rest of the directories".
	files := []FileInfo{
		{Name: "nested1", Kind: KindDir, ModTime: 100},
		{Name: "nested2", Kind: KindDir, ModTime: 200},
		{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	// Viewing a subdir: relPath = "subdir"
	html, err := RenderPage("subdir", "./", "./_thumbs/", "subdir", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 1. The Up entry must be the FIRST ROW of the dirs table
	//    (Phase 72: moved from a separate <div class="up-chip-row">
	//    above the table to a <tr class="up-row"> inside the
	//    table's <tbody>).
	upRowStart := strings.Index(html, `<table class="up-row-table">`)
	if upRowStart < 0 {
		t.Fatal(`expected a <table class="up-row-table"> containing the Up entry`)
	}
	upRowEnd := strings.Index(html[upRowStart:], `</tr>`)
	if upRowEnd < 0 {
		t.Fatal(`could not find end of up-row-table`)
	}
	upRow := html[upRowStart : upRowStart+upRowEnd]
	// The Up row should have a single <td colspan="2"> spanning
	// all 2 columns of the dirs table (Phase 77: Type column
	// removed, so the table is now 2 columns instead of 3).
	if !strings.Contains(upRow, `colspan="2"`) {
		t.Error("expected up-row to have a single td with colspan=\"3\"")
	}
	if !strings.Contains(upRow, `href="../"`) {
		t.Error("expected Up entry to link to '../'")
	}
	// relPath is "subdir" (top-level), so parent dir name is "" (the
	// gallery root). The chip should read "Up (../)".
	if !strings.Contains(upRow, "Up (../)") {
		t.Error(`expected 'Up (../)' text in the up-chip-row (top-level subdir, parent name empty)`)
	}
	// And should NOT have an empty parent dir like "Up (../ )"
	if strings.Contains(upRow, "Up (../ )") {
		t.Error(`expected no space before ')' in 'Up (../)' (template should render empty ParentDir as nothing)`)
	}
	if !strings.Contains(upRow, ">↑</span>") {
		t.Error("expected ↑ arrow icon for the Up entry")
	}
	if !strings.Contains(upRow, ">📁</span>") {
		t.Error("expected 📁 folder icon for the Up entry")
	}

	// 2. The subdirs must be in a <table class="files-table dirs-table">
	//    AFTER the up-chip-row. Per Phase 69, the chip-row layout
	//    was replaced with a full-width table.
	dirsTableStart := strings.Index(html, `<table class="files-table dirs-table">`)
	if dirsTableStart < 0 {
		t.Fatal(`expected a <table class="files-table dirs-table"> containing the subdirs`)
	}
	// Per Phase 72: the up-row is now INSIDE the dirs-table
	// (it's the first <tr> in the <tbody>). The dirsTable
	// starts before the up-row now.
	// Per Phase 76: the up-row is in a SEPARATE table now, so
	// it should appear BEFORE the dirs-table (not inside it).
	if upRowStart > dirsTableStart {
		t.Error("expected up-row-table to be BEFORE the dirs-table (Phase 76: separate table)")
	}
	dirsTableEnd := strings.Index(html[dirsTableStart:], `</table>`)
	if dirsTableEnd < 0 {
		t.Fatal(`could not find end of dirs-table`)
	}
	dirsTable := html[dirsTableStart : dirsTableStart+dirsTableEnd]
	// Per Phase 72: the Up entry is now a row INSIDE the dirs
	// table (not a separate chip above it). The dirs-table
	// Per Phase 76: the up entry is in a SEPARATE up-row-table
	// (above the dirs-table), not inside the dirs-table. So the
	// dirs-table contains only the subdirs, not the up entry's
	// href. The up entry's href is in a sibling element.
	if strings.Contains(dirsTable, `href="../"`) {
		t.Error(`expected NO href="../" in dirs-table (Phase 76: up entry is in separate up-row-table)`)
	}
	if !strings.Contains(dirsTable, "nested1/") {
		t.Error("expected nested1 subdir in dirs-table")
	}
	if !strings.Contains(dirsTable, "nested2/") {
		t.Error("expected nested2 subdir in dirs-table")
	}
	// Each subdir should be in its own <tr> with the directory
	// name in a Name cell (a .col-name <td>). With the up-row
	// moved to a separate table, the dirs-table has 2 subdir
	// rows + 1 thead row = 3 <tr> elements.
	rowCount := strings.Count(dirsTable, "<tr")
	if rowCount < 3 {
		t.Errorf("expected at least 3 <tr...> rows in dirs-table (1 thead + 2 subdirs), got %d", rowCount)
	}

	// 3. The dirs-row should NOT contain the images (the image
	//    grid is a separate section, comes after the dirs
	//    section in the page).
	othersIdx := strings.Index(html, "Other files")
	if othersIdx < 0 {
		othersIdx = len(html)
	}
	dirsSection := html[:othersIdx]
	// Per Phase 76: the up-row is in a separate up-row-table
	// (between the heading and the dirs-table), so it's a
	// SIBLING of the dirs section's dirs-table, not a child
	// of the dirs section. The dirs section only contains
	// the dirs-table itself.
	if !strings.Contains(dirsSection, `class="files-table dirs-table"`) {
		t.Error(`expected dirs section to contain the dirs-table`)
	}
	// The dirs section should NOT contain an up-row or
	// up-row-table (the up-row is in a sibling element).
	if strings.Contains(dirsSection, `class="up-row"`) {
		t.Error(`expected NO up-row in dirs section (Phase 76: in separate up-row-table)`)
	}
	// Old up-chip-row should be GONE.
	if strings.Contains(dirsSection, `class="up-chip-row"`) {
		t.Error(`expected NO up-chip-row in dirs section (replaced by up-row-table in Phase 76)`)
	}
	// And the up-row-table SHOULD be in the HTML (just not
	// inside the dirs section).
	if !strings.Contains(html, `<table class="up-row-table">`) {
		t.Error(`expected <table class="up-row-table"> in the rendered HTML (Phase 76)`)
	}
}

// TestRenderPage_DirsRowNoGap verifies that the subdirs row
// uses gap:0 (no spacing between chips) per the user's
// 2026-06-17 spec. We check by looking for the CSS rule in
// the rendered page (the CSS is in the <style> block in the
// <head>).
// TestRenderPage_DirsAsTable verifies the Phase 69 change:
// subdirs are now rendered as a full-width table (not a
// chip row). We check the rendered HTML for the
// <table class="files-table dirs-table"> structure and the
// CSS rule for .files-table (which controls the new layout).
// The old .dirs-section .dirs-row rule was removed in Phase 69.
func TestRenderPage_DirsAsTable(t *testing.T) {
	files := []FileInfo{
		{Name: "dir1", Kind: KindDir},
		{Name: "dir2", Kind: KindDir},
		{Name: "dir3", Kind: KindDir},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The new structure: a <table class="files-table dirs-table">
	// with one <tr> per directory.
	if !strings.Contains(html, `<table class="files-table dirs-table">`) {
		t.Error("expected <table class=\"files-table dirs-table\"> in the rendered page")
	}
	// The CSS rule for .files-table (the new layout).
	if !strings.Contains(html, ".files-table {") {
		t.Error("expected .files-table CSS rule in the rendered page")
	}
	// Old structure should be GONE: no <div class="dirs-row">
	// (the chip-row layout that was replaced in Phase 69).
	if strings.Contains(html, `<div class="dirs-row">`) {
		t.Error("expected NO <div class=\"dirs-row\"> in the rendered page (replaced by table in Phase 69)")
	}
	// Each subdir name should appear in a Name cell with a link.
	rowCount := strings.Count(html, "<tr>")
	if rowCount < 3 {
		t.Errorf("expected at least 3 <tr> rows (one per subdir), got %d", rowCount)
	}
}

func TestRenderPage_NoUpEntryAtRoot(t *testing.T) {
	// At the gallery root, no ".." entry should appear.
	files := []FileInfo{
		{Name: "nested1", Kind: KindDir},
		{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	html, err := RenderPage("root", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
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

// TestScanner_SymlinkToDirIsKindDir verifies that a symlink whose
// target is a directory is classified as KindDir, NOT as KindOther
// (which would put it in the "Other files" section). The user's
// filesystem has symlinks pointing at directories that were being
// misclassified because os.DirEntry.Info() uses Lstat under the
// hood — it returns the FileInfo of the link itself, not the target.
// The scanner now explicitly follows symlinks via os.Stat.
func TestScanner_SymlinkToDirIsKindDir(t *testing.T) {
	dir := t.TempDir()
	// Real target directory
	realDir := filepath.Join(dir, "real-subdir")
	if err := os.Mkdir(realDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Symlink pointing at the dir, with a .txt extension (the
	// "looks like a file" case — we want to make sure the
	// extension doesn't override the dir classification).
	if err := os.Symlink(realDir, filepath.Join(dir, "looks-like-file.txt")); err != nil {
		t.Skipf("symlinks not supported on this fs: %v", err)
	}
	// Symlink to a real image — should be classified as KindImage.
	realImg := filepath.Join(dir, "real.jpg")
	if err := os.WriteFile(realImg, []byte("fake-jpg"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realImg, filepath.Join(dir, "image-link.png")); err != nil {
		t.Skipf("symlinks not supported on this fs: %v", err)
	}
	// Broken symlink — should be silently skipped.
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), filepath.Join(dir, "broken")); err != nil {
		t.Skipf("symlinks not supported on this fs: %v", err)
	}

	s := NewScanner(dir)
	got, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	kindsByName := map[string]FileKind{}
	for _, f := range got {
		kindsByName[f.Name] = f.Kind
	}
	if kindsByName["looks-like-file.txt"] != KindDir {
		t.Errorf("symlink to dir: got %q, want %q", kindsByName["looks-like-file.txt"], KindDir)
	}
	if kindsByName["image-link.png"] != KindImage {
		t.Errorf("symlink to image: got %q, want %q", kindsByName["image-link.png"], KindImage)
	}
	if _, ok := kindsByName["broken"]; ok {
		t.Error("broken symlink should be skipped, but it appeared in the scan result")
	}
	if kindsByName["real-subdir"] != KindDir {
		t.Errorf("real dir: got %q, want %q", kindsByName["real-subdir"], KindDir)
	}
	if kindsByName["real.jpg"] != KindImage {
		t.Errorf("real image: got %q, want %q", kindsByName["real.jpg"], KindImage)
	}
}

// TestWriteBundledTemplates verifies the "make templates
// discoverable" behavior: on first run, the bundled template
// is written to the templates dir; on subsequent runs (or if
// the operator created a file), the existing file is NOT
// overwritten. Also covers the cleanup of the pre-inlining
// style.css/lightbox.js files (Phase 17).
func TestWriteBundledTemplates(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GALLERY_TEMPLATES_DIR", dir)

	// Seed the dir with stale style.css + lightbox.js (leftovers
	// from a pre-inlining install). writeBundledTemplates should
	// remove them on the first call.
	for _, stale := range []string{"style.css", "lightbox.js"} {
		path := filepath.Join(dir, stale)
		if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
			t.Fatalf("seed %s: %v", stale, err)
		}
	}

	// First call: should write gallery.tmpl AND remove the
	// stale style.css/lightbox.js.
	if err := writeBundledTemplates(); err != nil {
		t.Fatalf("first writeBundledTemplates: %v", err)
	}

	// gallery.tmpl exists, is non-empty.
	tmplPath := filepath.Join(dir, "gallery.tmpl")
	info, err := os.Stat(tmplPath)
	if err != nil {
		t.Errorf("expected gallery.tmpl to exist after first call, got stat err: %v", err)
	} else if info.Size() == 0 {
		t.Error("gallery.tmpl was written but is empty")
	}

	// style.css and lightbox.js should be gone (cleanup).
	for _, name := range []string{"style.css", "lightbox.js"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("stale %s should have been removed; still present", name)
		}
	}

	// Second call: should NOT overwrite the existing gallery.tmpl.
	// Mutate it to a known marker string, call again, assert the
	// marker is preserved.
	mutated := []byte("OPERATOR OVERRIDE\n")
	if err := os.WriteFile(tmplPath, mutated, 0o644); err != nil {
		t.Fatalf("mutate gallery.tmpl: %v", err)
	}
	if err := writeBundledTemplates(); err != nil {
		t.Fatalf("second writeBundledTemplates: %v", err)
	}
	after, _ := os.ReadFile(tmplPath)
	if string(after) != string(mutated) {
		t.Errorf("gallery.tmpl was overwritten by the bundled template; expected operator override to survive")
	}

	// Cleanup: verify no .tmp files left behind (atomic write).
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover .tmp file: %s", e.Name())
		}
	}
}

// TestSplitFiles_DirsAlwaysAlphabetical verifies that the directory
// strip is always in case-insensitive alphabetical order, regardless
// of the order the scanner returned or the user's image-sort choice.
// Per user spec 2026-06-14: "the directory list should be in
// alphabetical order, and if any ordering is applied to the images,
// this will not affect the directory listing."
func TestSplitFiles_DirsAlwaysAlphabetical(t *testing.T) {
	now := time.Date(2026, 6, 12, 14, 30, 0, 0, time.UTC)
	// Feed dirs in a non-alphabetical order (which is what the
	// scanner would produce if it's sorted by mtime desc).
	files := []FileInfo{
		{Name: "zeta-dir", ModTime: now.UnixNano(), Size: 0, Kind: KindDir},
		{Name: "alpha-dir", ModTime: now.Add(-1 * time.Hour).UnixNano(), Size: 0, Kind: KindDir},
		{Name: "MIDDLE-dir", ModTime: now.Add(-2 * time.Hour).UnixNano(), Size: 0, Kind: KindDir},
		{Name: "beta-dir", ModTime: now.Add(-3 * time.Hour).UnixNano(), Size: 0, Kind: KindDir},
		// And some images / others mixed in, to confirm splitFiles
		// only re-sorts the dirs.
		{Name: "zebra.jpg", ModTime: now.Add(-4 * time.Hour).UnixNano(), Size: 100, Kind: KindImage},
		{Name: "apple.jpg", ModTime: now.Add(-5 * time.Hour).UnixNano(), Size: 200, Kind: KindImage},
		{Name: "notes.txt", ModTime: now.Add(-6 * time.Hour).UnixNano(), Size: 50, Kind: KindOther},
	}
	dirs, _, _ := splitFiles(files)
	want := []string{"alpha-dir", "beta-dir", "MIDDLE-dir", "zeta-dir"}
	if len(dirs) != len(want) {
		t.Fatalf("got %d dirs, want %d", len(dirs), len(want))
	}
	for i, d := range dirs {
		if d.Name != want[i] {
			t.Errorf("dirs[%d].Name = %q, want %q (full order: %v)",
				i, d.Name, want[i], gotNames(dirs))
		}
	}
}

// TestSplitFiles_DirsUnaffectedByImageSort is a higher-level test:
// pass in a file list whose dirs are intentionally out of alpha
// order, run them through RenderPage with various image-sort
// settings, and confirm the dirs come out in the same alphabetical
// order regardless.
func TestSplitFiles_DirsUnaffectedByImageSort(t *testing.T) {
	now := time.Date(2026, 6, 12, 14, 30, 0, 0, time.UTC)
	files := []FileInfo{
		{Name: "zebra-dir", ModTime: now.Add(-10 * time.Hour).UnixNano(), Size: 0, Kind: KindDir},
		{Name: "alpha-dir", ModTime: now.Add(-20 * time.Hour).UnixNano(), Size: 0, Kind: KindDir},
		{Name: "yankee.jpg", ModTime: now.Add(-30 * time.Hour).UnixNano(), Size: 100, Kind: KindImage},
		{Name: "bravo.jpg", ModTime: now.Add(-40 * time.Hour).UnixNano(), Size: 200, Kind: KindImage},
	}
	for _, sortSpec := range []string{"mtime", "name", "size"} {
		for _, order := range []string{"asc", "desc"} {
			q := url.Values{}
			q.Set("sort", sortSpec)
			q.Set("order", order)
			html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, q, nil, nil)
			if err != nil {
				t.Fatalf("sort=%s order=%s: %v", sortSpec, order, err)
			}
			// Find the positions of the two dir names in the HTML
			// and confirm alpha-dir comes before zebra-dir.
			alphaPos := strings.Index(html, "alpha-dir")
			zebraPos := strings.Index(html, "zebra-dir")
			if alphaPos < 0 || zebraPos < 0 {
				t.Fatalf("sort=%s order=%s: dir names not found in HTML", sortSpec, order)
			}
			if alphaPos > zebraPos {
				t.Errorf("sort=%s order=%s: dirs NOT alphabetical (alpha-dir at %d, zebra-dir at %d)",
					sortSpec, order, alphaPos, zebraPos)
			}
		}
	}
}

func gotNames(files []FileInfo) []string {
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name
	}
	return names
}

// TestRenderPage_VideoThumbnailRendering verifies the Phase 62
// behavior: when video thumb generation is enabled (the default
// — noVideoThumbs=false), videos get a ThumbURL set, and the
// rendered card contains an <img class="thumb-img"> element
// pointing at that thumb URL, plus the play overlay. When
// noVideoThumbs=true, the ThumbURL is empty, no <img class="thumb-img">
// is rendered, and only the play overlay + placeholder gradient
// are shown.
func TestRenderPage_VideoThumbnailRendering(t *testing.T) {
	files := []FileInfo{
		{Name: "clip.mp4", ModTime: 3, Size: 9999, Kind: KindVideo},
	}

	t.Run("video thumb enabled (noVideoThumbs=false) → <img class=\"thumb-img\"> is rendered", func(t *testing.T) {
		html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(html, `class="thumb-img"`) {
			t.Error("expected <img class=\"thumb-img\"> for video when noVideoThumbs=false")
		}
		if !strings.Contains(html, `src="./_thumbs/clip.webp"`) {
			t.Error("expected ThumbURL to be set to ./_thumbs/clip.webp for video")
		}
		if !strings.Contains(html, `class="play-overlay"`) {
			t.Error("expected play-overlay to be present alongside the thumb img")
		}
	})

	t.Run("video thumb disabled (noVideoThumbs=true) → no <img class=\"thumb-img\">, placeholder shown", func(t *testing.T) {
		html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, true, 0, files, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(html, `class="thumb-img"`) {
			t.Error("expected NO <img class=\"thumb-img\"> when noVideoThumbs=true")
		}
		if strings.Contains(html, `src="./_thumbs/clip.webp"`) {
			t.Error("expected ThumbURL to be empty when noVideoThumbs=true")
		}
		// The play overlay should still be there (videos still
		// display, just without a real thumbnail).
		if !strings.Contains(html, `class="play-overlay"`) {
			t.Error("expected play-overlay to still be present even when video thumb is disabled")
		}
		// The placeholder gradient (.thumb-video background) is
		// still in the CSS — verifying its presence here is a
		// sanity check that we didn't accidentally remove it.
		if !strings.Contains(html, `thumb-video`) {
			t.Error("expected the .thumb-video class to be present for the placeholder gradient")
		}
	})

	t.Run("image (KindImage) is unaffected by noVideoThumbs", func(t *testing.T) {
		files := []FileInfo{
			{Name: "photo.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		}
		// With noVideoThumbs=true: images should STILL get their
		// thumb URL (noVideoThumbs only affects videos).
		html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, true, 0, files, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(html, `src="./_thumbs/photo.webp"`) {
			t.Error("image thumb URL should be set regardless of noVideoThumbs (it's an image, not a video)")
		}
	})
}

func TestRenderPage_OpenButtonOnImageAndVideoTiles(t *testing.T) {
	// Each image/video tile should have an "open in new tab" button
	// (a <span class="open-btn" role="button">) positioned in the
	// thumb. The button's data-open-url should be the tile's href.
	now := time.Date(2026, 6, 12, 14, 30, 0, 0, time.UTC)
	files := []FileInfo{
		{Name: "photo.jpg", ModTime: now.UnixNano(), Size: 100, Kind: KindImage},
		{Name: "clip.mp4", ModTime: now.UnixNano(), Size: 9999, Kind: KindVideo},
		{Name: "notes.txt", ModTime: now.UnixNano(), Size: 50, Kind: KindOther},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Should have exactly 2 open-btns (one per image/video tile — NOT on the other-files chip).
	if c := strings.Count(html, `class="open-btn"`); c != 2 {
		t.Errorf("expected 2 open-btns (one per image/video tile), got %d", c)
	}
	// Each open-btn should have the right a11y attributes.
	for _, want := range []string{
		`role="button"`,
		`tabindex="0"`,
		`title="Open in new tab"`,
		`aria-label="Open in new tab"`,
		`data-open-url="./photo.jpg"`,
		`data-open-url="./clip.mp4"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("open-btn missing %q", want)
		}
	}
	// The open-btn should be inside the .thumb (not in the .other-files strip).
	othersIdx := strings.Index(html, "Other files")
	imagesIdx := strings.Index(html, ">Media (")
	if imagesIdx < 0 {
		t.Fatal("could not find media section")
	}
	imagesSection := html[imagesIdx:]
	if !strings.Contains(imagesSection, `class="open-btn"`) {
		t.Error("expected open-btn to be in the image grid section")
	}
	// Other-files chips should NOT have an open-btn.
	othersSection := html[othersIdx:imagesIdx]
	if strings.Contains(othersSection, `class="open-btn"`) {
		t.Error("open-btn should not be in the 'Other files' section (per user spec — only on image/video tiles)")
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

func TestRenderPage_GoogleStylePagination(t *testing.T) {
	// 200 images at pageSize=8 -> 25 pages (well past the
	// <= 10 threshold, so the Google ellipsis pattern kicks in).
	files25 := make([]FileInfo, 200)
	for i := 0; i < 200; i++ {
		files25[i] = FileInfo{Name: imageName(i), ModTime: int64(i), Size: 1024, Kind: KindImage}
	}
	cases := []struct {
		name        string
		currentPage int
		wantPages   []int
	}{
		{
			name:        "25 pages, current=1 (near start): 1 2 3 4 5 ... 25",
			currentPage: 1,
			wantPages:   []int{1, 2, 3, 4, 5, 0, 25},
		},
		{
			name:        "25 pages, current=13 (middle): 1 ... 12 13 14 ... 25",
			currentPage: 13,
			wantPages:   []int{1, 0, 12, 13, 14, 0, 25},
		},
		{
			name:        "25 pages, current=25 (near end): 1 ... 21 22 23 24 25",
			currentPage: 25,
			wantPages:   []int{1, 0, 21, 22, 23, 24, 25},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := url.Values{"page": {strconv.Itoa(tc.currentPage)}}
			html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 8, files25, q, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			navStart := strings.Index(html, `<nav class="pagination">`)
			if navStart < 0 {
				t.Fatal(`expected <nav class="pagination"> in the page`)
			}
			navEnd := strings.Index(html[navStart:], `</nav>`)
			nav := html[navStart : navStart+navEnd]
			// Verify pages appear in order
			lastIdx := 0
			for _, p := range tc.wantPages {
				var want string
				if p == 0 {
					want = "page-ellipsis"
				} else {
					want = `>` + strconv.Itoa(p) + `<`
				}
				idx := strings.Index(nav[lastIdx:], want)
				if idx < 0 {
					t.Errorf("expected %q in pagination nav (not found), got: %q", want, nav)
					break
				}
				lastIdx += idx + len(want)
			}
			// Verify the current page has the active class
			currentStr := strconv.Itoa(tc.currentPage)
			if !strings.Contains(nav, `class="page-btn active">`+currentStr+`<`) {
				t.Errorf("expected current page %d to have 'page-btn active' class in nav: %q", tc.currentPage, nav)
			}
		})
	}

	// 4-page case (≤ 10 -> show all, no ellipsis)
	files4 := make([]FileInfo, 200)
	for i := 0; i < 200; i++ {
		files4[i] = FileInfo{Name: imageName(i), ModTime: int64(i), Size: 1024, Kind: KindImage}
	}
	q := url.Values{"page": {"2"}}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 50, files4, q, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	navStart := strings.Index(html, `<nav class="pagination">`)
	if navStart < 0 {
		t.Fatal(`expected <nav class="pagination"> in the page`)
	}
	navEnd := strings.Index(html[navStart:], `</nav>`)
	nav := html[navStart : navStart+navEnd]
	for _, p := range []int{1, 2, 3, 4} {
		want := `>` + strconv.Itoa(p) + `<`
		if !strings.Contains(nav, want) {
			t.Errorf("expected page %d in nav (≤ 10 pages, no ellipsis), got: %q", p, nav)
		}
	}
	if strings.Contains(nav, "page-ellipsis") {
		t.Errorf("expected NO ellipsis for ≤ 10 pages, got: %q", nav)
	}
}

// TestRenderPage_HeaderShowsPagePosition verifies that the
// header meta line shows "Page X of Y" after the per-page
// indicator (only when multi-page). Per user request 2026-06-17:
// add the current page and display 'Page 1 of N' in the
// header as well.
func TestRenderPage_HeaderShowsPagePosition(t *testing.T) {
	files := make([]FileInfo, 200)
	for i := 0; i < 200; i++ {
		files[i] = FileInfo{Name: imageName(i), ModTime: int64(i), Size: 1024, Kind: KindImage}
	}
	// 200 images, pageSize=50 -> 4 pages. Page 2 of 4.
	q := url.Values{"page": {"2"}}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 50, files, q, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	metaStart := strings.Index(html, `class="meta"`)
	metaEnd := strings.Index(html[metaStart:], `</div>`)
	metaBlock := html[metaStart : metaStart+metaEnd]
	if !strings.Contains(metaBlock, "Page 2 of 4") {
		t.Errorf("expected 'Page 2 of 4' in header meta block, got: %q", metaBlock)
	}
	// The 'N pages' indicator was removed in Phase 37 per user
	// request; only the 'Page X of Y' indicator remains. Make
	// sure the old indicator is NOT in the output anymore.
	if strings.Contains(metaBlock, "4 pages") {
		t.Errorf("expected NO '4 pages' indicator (removed in Phase 37), got: %q", metaBlock)
	}
	if !strings.Contains(metaBlock, "50 per page") {
		t.Errorf("expected '50 per page' in header meta block, got: %q", metaBlock)
	}
	// Per Phase 43: the size is now on OTHER FILES (not images).
	// Images show just the count. To exercise the other-files
	// size path, the test would need KindOther files (we don't
	// add them here since the original test was about pagination).
	if strings.Contains(metaBlock, "images (") {
		t.Errorf("expected 'N images' (no size — size moved to other files in Phase 43), got: %q", metaBlock)
	}
	// Order check: per-page -> Page X of Y (no more 'N pages' in between)
	perPageIdx := strings.Index(metaBlock, "50 per page")
	pageOfIdx := strings.Index(metaBlock, "Page 2 of 4")
	if !(perPageIdx < pageOfIdx) {
		t.Errorf("expected order '50 per page' < 'Page 2 of 4' in meta block, got: %q", metaBlock)
	}
}

// TestRenderPage_TotalAllFilesSize verifies the header meta
// shows the pre-formatted total size of ALL files (images +
// other files) in a separate segment wrapped in //
// separators. Per user request 2026-06-18 (Phase 44):
//
//	"the X.X KB is the total for all files in the directory"
//
// The size is shown as `// (size) //` between the file
// counts and the directories count, visually distinct from
// the regular `·` separator. Pre-formatted via humanSize() —
// B / KB / MB / GB.
func TestRenderPage_TotalAllFilesSize(t *testing.T) {
	// Use a mix of KindImage and KindOther files to verify
	// the size covers BOTH types (not just other files like
	// Phase 43, not just images like Phase 37).
	cases := []struct {
		name       string
		imageSizes []int64
		otherSizes []int64
		wantTotal  string
	}{
		{
			name:       "small mix: 1 image (500 B) + 2 others (500 B) = 1.5 KB",
			imageSizes: []int64{500},
			otherSizes: []int64{500, 500},
			wantTotal:  "1.5 KB",
		},
		{
			name:       "kilobyte-range: 5 * 1 KB images = 5.0 KB",
			imageSizes: []int64{1024, 1024, 1024, 1024, 1024},
			otherSizes: nil,
			wantTotal:  "5.0 KB",
		},
		{
			name:       "megabyte-range: 100 images * 100 KB + 10 others * 1 KB",
			imageSizes: make100KB(100),
			otherSizes: []int64{1024, 1024, 1024, 1024, 1024, 1024, 1024, 1024, 1024, 1024},
			wantTotal:  "9.8 MB", // (100*100KB + 10*1KB) = ~9.8 MB
		},
		{
			// 1000 images * 1000 MB each = 976.56 GB total
			// (humanSize uses 1024-based units, not 1000-based).
			name:       "gigabyte-range: 1000 * 1000 MB (=> 976.56 GB)",
			imageSizes: make1000MB(1000),
			otherSizes: nil,
			wantTotal:  "976.56 GB",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var files []FileInfo
			for i, s := range tc.imageSizes {
				files = append(files, FileInfo{Name: fmt.Sprintf("img-%d.jpg", i), ModTime: int64(i), Size: s, Kind: KindImage})
			}
			for i, s := range tc.otherSizes {
				files = append(files, FileInfo{Name: fmt.Sprintf("meta-%d.json", i), ModTime: int64(i + 1000), Size: s, Kind: KindOther})
			}
			html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			metaStart := strings.Index(html, `class="meta"`)
			metaEnd := strings.Index(html[metaStart:], `</div>`)
			metaBlock := html[metaStart : metaStart+metaEnd]
			// The size segment is rendered as `//<size>//` with
			// `//` separators on both sides (each in its own
			// <span> for the visual `·` vs `//` distinction).
			// The browser adds visual spacing via the flex
			// `gap: 0.5rem` on the parent .meta, so the
			// rendered text is `// (size) //` with gaps.
			// We assert the size string is present in parens,
			// AND the `//` separator appears on both sides.
			wantParens := fmt.Sprintf("(%s total)", tc.wantTotal)
			if !strings.Contains(metaBlock, wantParens) {
				t.Errorf("expected header to contain %q, got: %q", wantParens, metaBlock)
			}
			if strings.Count(metaBlock, "//</span>") < 2 {
				t.Errorf("expected at least 2 `//` separator spans in header, got: %q", metaBlock)
			}
			// The old per-count size formats should NOT appear.
			if strings.Contains(metaBlock, "images (") || strings.Contains(metaBlock, "other files (") {
				t.Errorf("expected NO size attached to file counts (size is a separate segment now), got: %q", metaBlock)
			}
		})
	}
}

// Helpers for TestRenderPage_TotalAllFilesSize
func make100KB(n int) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = 100 * 1024
	}
	return out
}

func make1000MB(n int) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = 1000 * 1024 * 1024
	}
	return out
}

// TestBundledTemplate_LightboxSupportsVideo verifies that the
// lightbox JS in the bundled template supports both images
// (via document.createElement('img')) and videos (via
// document.createElement('video')). Per user request 2026-06-17:
// "is there a way for videos to also play in a lightbox - isn't
// there an html element?"
//
// This test extracts the <script> block from the bundled
// template and checks for the presence of the video-supporting
// code paths. We don't run the JS (no DOM in tests); we just
// check for the syntactic evidence that videos are supported.
func TestBundledTemplate_LightboxSupportsVideo(t *testing.T) {
	// Read the bundled template by parsing the galleryTemplate
	// constant. We do this via the same path the live system
	// uses (loadTemplate) so we get the actual content rendered.
	tmpl, err := loadTemplate("")
	if err != nil {
		t.Fatal(err)
	}
	// Render an empty data to get the template content
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, PageData{Title: "test"}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	// The bundled template should contain code that creates a
	// <video> element for video tiles
	if !strings.Contains(html, "createElement('video')") {
		t.Error("expected bundled template JS to create a <video> element for video tiles")
	}
	// Should also still create <img> for image tiles (not
	// removed the image path)
	if !strings.Contains(html, "createElement('img')") {
		t.Error("expected bundled template JS to still create an <img> element for image tiles")
	}
	// The card filter should now include videos (was: only
	// cards with <img> child; should be: cards with <img> OR
	// .video class)
	if !strings.Contains(html, "c.classList.contains('video')") {
		t.Error("expected bundled template JS to include .video class in the card filter (videos in the lightbox)")
	}
	// The video element should have controls (browser-native
	// play/pause/seek UI)
	if !strings.Contains(html, "v.controls = true") {
		t.Error("expected bundled template JS to set controls=true on the video element")
	}
	// The clear() function should pause the video before
	// removing it (so audio doesn't keep playing in the
	// background after the lightbox closes)
	if !strings.Contains(html, "currentEl.pause()") {
		t.Error("expected bundled template JS to call currentEl.pause() in clear() (stop video on close/navigate)")
	}
	// The CSS should style both img and video in the lightbox
	// (max-width, max-height, object-fit, etc.)
	if !strings.Contains(html, "#gallery-lightbox video") {
		t.Error("expected bundled template CSS to style #gallery-lightbox video (size constraints match img)")
	}
}

// TestBundledTemplate_LightboxJSValidSyntax is a defensive
// check: extract the <script> block from the bundled template
// and pipe it through `node --check` to verify it's
// syntactically valid JS. Catches typos introduced by future
// template edits. Skipped if `node` isn't on PATH.
func TestBundledTemplate_LightboxJSValidSyntax(t *testing.T) {
	tmpl, err := loadTemplate("")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, PageData{Title: "test"}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	// Extract the <script>...</script> block
	sStart := strings.Index(html, "<script>")
	sEnd := strings.Index(html, "</script>")
	if sStart < 0 || sEnd < 0 || sEnd < sStart {
		t.Fatal("expected a <script>...</script> block in the bundled template")
	}
	js := html[sStart+len("<script>") : sEnd]
	// Check if `node` is on PATH
	_, err = exec.LookPath("node")
	if err != nil {
		t.Skipf("node not on PATH; skipping JS syntax check (extracted %d chars)", len(js))
	}
	// Write to a temp file and check syntax
	tmp, err := os.CreateTemp(t.TempDir(), "lightbox-*.js")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write([]byte(js)); err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	cmd := exec.Command("node", "--check", tmp.Name())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("node --check failed on the bundled lightbox JS:\n%s\nerror: %v", out, err)
	}
}

// TestRenderPage_HeaderSeparatesImageAndVideoCounts verifies that
// the header meta line shows the image count and video count
// separately, so videos are not miscounted as images. Per
// user request 2026-06-17: "Add a 'video' indicator in the
// header sort UI".
func TestRenderPage_HeaderSeparatesImageAndVideoCounts(t *testing.T) {
	// 5 images + 2 videos = 7 media total
	files := []FileInfo{
		{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "b.jpg", ModTime: 2, Size: 100, Kind: KindImage},
		{Name: "c.jpg", ModTime: 3, Size: 100, Kind: KindImage},
		{Name: "d.jpg", ModTime: 4, Size: 100, Kind: KindImage},
		{Name: "e.jpg", ModTime: 5, Size: 100, Kind: KindImage},
		{Name: "clip1.mp4", ModTime: 6, Size: 1024, Kind: KindVideo},
		{Name: "clip2.mp4", ModTime: 7, Size: 2048, Kind: KindVideo},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Find the header meta div
	metaStart := strings.Index(html, `class="meta"`)
	if metaStart < 0 {
		t.Fatal("expected meta div in the header")
	}
	metaEnd := strings.Index(html[metaStart:], `</div>`)
	if metaEnd < 0 {
		t.Fatal("could not find end of meta div")
	}
	metaBlock := html[metaStart : metaStart+metaEnd]
	// Should show "5 images" (NOT "7 images" — that was the
	// misleading old behavior)
	if !strings.Contains(metaBlock, "5 images") {
		t.Errorf("expected '5 images' in the header meta block, got: %q", metaBlock)
	}
	if strings.Contains(metaBlock, "7 images") {
		t.Errorf("expected NOT to see '7 images' (videos should be separate), got: %q", metaBlock)
	}
	// Should show "2 videos"
	if !strings.Contains(metaBlock, "2 videos") {
		t.Errorf("expected '2 videos' in the header meta block, got: %q", metaBlock)
	}

	// Zero videos: should NOT show the videos indicator at all
	filesNoVideo := []FileInfo{
		{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "b.jpg", ModTime: 2, Size: 100, Kind: KindImage},
	}
	html2, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, filesNoVideo, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	metaStart2 := strings.Index(html2, `class="meta"`)
	metaEnd2 := strings.Index(html2[metaStart2:], `</div>`)
	metaBlock2 := html2[metaStart2 : metaStart2+metaEnd2]
	if strings.Contains(metaBlock2, "videos") {
		t.Errorf("expected NO 'videos' indicator when there are 0 videos, got: %q", metaBlock2)
	}
	if !strings.Contains(metaBlock2, "2 images") {
		t.Errorf("expected '2 images' with no videos, got: %q", metaBlock2)
	}

	// All videos (zero images): should show "0 images · N videos"
	filesAllVideo := []FileInfo{
		{Name: "v1.mp4", ModTime: 1, Size: 1024, Kind: KindVideo},
		{Name: "v2.mp4", ModTime: 2, Size: 2048, Kind: KindVideo},
		{Name: "v3.mp4", ModTime: 3, Size: 4096, Kind: KindVideo},
	}
	html3, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, filesAllVideo, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	metaStart3 := strings.Index(html3, `class="meta"`)
	metaEnd3 := strings.Index(html3[metaStart3:], `</div>`)
	metaBlock3 := html3[metaStart3 : metaStart3+metaEnd3]
	if !strings.Contains(metaBlock3, "0 images") {
		t.Errorf("expected '0 images' with all-video directory, got: %q", metaBlock3)
	}
	if !strings.Contains(metaBlock3, "3 videos") {
		t.Errorf("expected '3 videos' with all-video directory, got: %q", metaBlock3)
	}
}

// TestRenderPage_UpEntryShowsParentDirName verifies that the up
// chip in a subdir shows the parent directory's name: e.g. when
// viewing "/photos/vacation/", the chip reads "Up (../photos)".
// At the gallery root or in a top-level subdir, the parent dir
// name is empty and the chip reads "Up (../)" with no trailing
// space. Per user request 2026-06-17.
func TestRenderPage_UpEntryShowsParentDirName(t *testing.T) {
	files := []FileInfo{
		{Name: "photo.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	cases := []struct {
		name     string
		relPath  string
		wantText string
	}{
		{
			name:     "gallery root (relPath empty) - no up entry, no parent name",
			relPath:  "",
			wantText: "",
		},
		{
			name:     "top-level subdir (parent is gallery root) - empty parent name",
			relPath:  "photos",
			wantText: "Up (../)",
		},
		{
			name:     "deeper subdir (parent is named photos)",
			relPath:  "photos/vacation",
			wantText: "Up (../photos)",
		},
		{
			name:     "even deeper (parent is named vacation)",
			relPath:  "photos/vacation/2024",
			wantText: "Up (../vacation)",
		},
		{
			// The bug case: when the URL has a trailing slash
			// (e.g. /images/photos/), relPath is "photos/" (with
			// trailing slash). Without the trailing-slash trim in
			// RenderPage, filepath.Dir("photos/") returns
			// "photos" (the CURRENT dir), so the up chip would
			// say "Up (../photos)" — same text as the current
			// dir, not the parent. With the trim, relPath is
			// normalized to "photos" and the parent is correctly
			// empty (top-level subdir).
			name:     "trailing slash on top-level subdir (parent is empty, NOT the current dir name)",
			relPath:  "photos/",
			wantText: "Up (../)",
		},
		{
			// Same trailing-slash bug for a deeper subdir:
			// without the trim, "photos/vacation/" would yield
			// "vacation" (current dir) instead of "photos" (parent).
			name:     "trailing slash on deeper subdir (parent is the dir above, NOT the current dir name)",
			relPath:  "photos/vacation/",
			wantText: "Up (../photos)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			html, err := RenderPage("test", "./", "./_thumbs/", tc.relPath, "", false, false, 0, files, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantText == "" {
				// Root view: no up entry at all
				if strings.Contains(html, `class="up-row"`) {
					t.Errorf("expected NO up-row at the gallery root, but found one")
				}
				// Old up-chip-row should be GONE.
				if strings.Contains(html, `class="up-chip-row"`) {
					t.Errorf("expected NO up-chip-row at the gallery root (replaced by up-row in Phase 72)")
				}
				return
			}
			upRowStart := strings.Index(html, `<table class="up-row-table">`)
			if upRowStart < 0 {
				t.Fatalf("expected an up-row-table for relPath %q", tc.relPath)
			}
			upRowEnd := strings.Index(html[upRowStart:], `</tr>`)
			upRow := html[upRowStart : upRowStart+upRowEnd]
			if !strings.Contains(upRow, tc.wantText) {
				t.Errorf("up-row-table for relPath %q: expected text %q, got: %q", tc.relPath, tc.wantText, upRow)
			}
		})
	}
}

// TestSortFiles_MtimeHonorsOrder verifies that sortFiles
// actually honors the `order` parameter for the "mtime" field.
// Per the bug reported 2026-06-17 by the user: "sort=mtime&order=asc
// is not working - it does not sort them". The previous code
// returned early for "mtime" (because the scanner already sorts
// by mtime desc), so the asc case was silently ignored.
func TestSortFiles_MtimeHonorsOrder(t *testing.T) {
	// Files in a deliberately shuffled order. By ModTime
	// (asc):  b=2, d=4, a=1, e=5, c=3
	// By ModTime (desc): e=5, d=4, c=3, b=2, a=1
	files := []FileInfo{
		{Name: "b.jpg", ModTime: 2, Size: 100, Kind: KindImage},
		{Name: "d.jpg", ModTime: 4, Size: 100, Kind: KindImage},
		{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "e.jpg", ModTime: 5, Size: 100, Kind: KindImage},
		{Name: "c.jpg", ModTime: 3, Size: 100, Kind: KindImage},
	}
	cases := []struct {
		name      string
		spec      SortSpec
		wantOrder []string
	}{
		{
			name:      "mtime asc: oldest first",
			spec:      SortSpec{Field: "mtime", Order: "asc"},
			wantOrder: []string{"a.jpg", "b.jpg", "c.jpg", "d.jpg", "e.jpg"},
		},
		{
			name:      "mtime desc: newest first",
			spec:      SortSpec{Field: "mtime", Order: "desc"},
			wantOrder: []string{"e.jpg", "d.jpg", "c.jpg", "b.jpg", "a.jpg"},
		},
		{
			name:      "mtime (default order=desc): newest first",
			spec:      SortSpec{Field: "mtime", Order: ""},
			wantOrder: []string{"e.jpg", "d.jpg", "c.jpg", "b.jpg", "a.jpg"},
		},
		{
			name:      "empty Field (defaults to mtime): newest first",
			spec:      SortSpec{Field: "", Order: "desc"},
			wantOrder: []string{"e.jpg", "d.jpg", "c.jpg", "b.jpg", "a.jpg"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Make a fresh copy of the shuffled input for each
			// sub-test (sortFiles mutates in place)
			input := make([]FileInfo, len(files))
			copy(input, files)
			sortFiles(input, tc.spec)
			got := make([]string, len(input))
			for i, f := range input {
				got[i] = f.Name
			}
			if !equalStrings(got, tc.wantOrder) {
				t.Errorf("expected %v, got %v", tc.wantOrder, got)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRenderPage_OtherFilesRespectSort verifies the Phase 70
// change: other files should respond to the user's sort
// selection (just like images do), while directories stay
// alphabetical regardless of the sort.
//
// We test by rendering the same files with different sort
// queries and verifying the ORDER of file names in the
// rendered HTML changes accordingly.
func TestRenderPage_OtherFilesRespectSort(t *testing.T) {
	// Files in a NON-alphabetical order so we can see if
	// sort took effect. Names: zebra.txt, apple.txt, mango.txt
	// (alphabetical: apple, mango, zebra)
	files := []FileInfo{
		{Name: "photo.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "zebra.txt", ModTime: 100, Size: 100, Kind: KindOther},
		{Name: "apple.txt", ModTime: 200, Size: 200, Kind: KindOther},
		{Name: "mango.txt", ModTime: 300, Size: 300, Kind: KindOther},
	}

	// Helper: extract the order of other files in the
	// rendered HTML by finding each <tr> in the others
	// section and pulling the first .table-link text.
	extractOrder := func(html string) []string {
		othersStart := strings.Index(html, "Other files")
		if othersStart < 0 {
			return nil
		}
		imgStart := strings.Index(html[othersStart:], ">Media (")
		if imgStart < 0 {
			return nil
		}
		othersSection := html[othersStart : othersStart+imgStart]
		var order []string
		// Find each <tr> in the others section.
		idx := 0
		for {
			trStart := strings.Index(othersSection[idx:], "<tr>")
			if trStart < 0 {
				break
			}
			trStart += idx
			trEnd := strings.Index(othersSection[trStart:], "</tr>")
			if trEnd < 0 {
				break
			}
			trEnd += trStart
			tr := othersSection[trStart:trEnd]
			// Extract the link text. The link contains an icon
			// span + the name, e.g.:
			//   <a class="table-link" href="./notes.txt">
			//     <span class="chip-icon">📄</span>notes.txt
			//   </a>
			// We need to skip past the icon span to get just "notes.txt".
			aStart := strings.Index(tr, "<a ")
			if aStart >= 0 {
				gtStart := strings.Index(tr[aStart:], ">")
				if gtStart >= 0 {
					contentStart := aStart + gtStart + 1
					// Skip past the </span> close of the icon span.
					spanEnd := strings.Index(tr[contentStart:], "</span>")
					if spanEnd >= 0 {
						contentStart += spanEnd + len("</span>")
					}
					// Now find the closing </a>.
					gtEnd := strings.Index(tr[contentStart:], "</a>")
					if gtEnd >= 0 {
						linkText := tr[contentStart : contentStart+gtEnd]
						order = append(order, strings.TrimSpace(linkText))
					}
				}
			}
			idx = trEnd + 1
		}
		return order
	}

	t.Run("sort=name,asc: others sorted alphabetically (apple, mango, zebra)", func(t *testing.T) {
		html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files,
			url.Values{"sort": []string{"name"}, "order": []string{"asc"}}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := extractOrder(html)
		want := []string{"apple.txt", "mango.txt", "zebra.txt"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("expected others order %v, got %v", want, got)
		}
	})

	t.Run("sort=name,desc: others sorted reverse-alpha (zebra, mango, apple)", func(t *testing.T) {
		html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files,
			url.Values{"sort": []string{"name"}, "order": []string{"desc"}}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := extractOrder(html)
		want := []string{"zebra.txt", "mango.txt", "apple.txt"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("expected others order %v, got %v", want, got)
		}
	})

	t.Run("sort=mtime,asc: others sorted by mtime asc (zebra, apple, mango)", func(t *testing.T) {
		// mtimes: zebra=100, apple=200, mango=300
		html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files,
			url.Values{"sort": []string{"mtime"}, "order": []string{"asc"}}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := extractOrder(html)
		want := []string{"zebra.txt", "apple.txt", "mango.txt"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("expected others order %v, got %v", want, got)
		}
	})

	t.Run("sort=size,asc: others sorted by size asc (zebra 100, apple 200, mango 300)", func(t *testing.T) {
		html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files,
			url.Values{"sort": []string{"size"}, "order": []string{"asc"}}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := extractOrder(html)
		want := []string{"zebra.txt", "apple.txt", "mango.txt"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("expected others order %v, got %v", want, got)
		}
	})
}

// TestRenderPage_DirectoriesIgnoreSort verifies the Phase 70
// behavior: directories stay alphabetical regardless of the
// sort selection. The user explicitly asked for this (2026-06-14):
// "the directory list should be in alphabetical order, and if
// any ordering is applied to the images, this will not affect
// the directory listing."
// TestLoadTemplate_CachesAcrossCalls verifies Phase 102:
// the parsed template is cached in a process-wide singleton and
// reused across calls. The same *template.Template pointer should
// be returned for repeated calls when the file mtime is unchanged,
// and a fresh template should be returned when the file mtime
// changes.
func TestLoadTemplate_CachesAcrossCalls(t *testing.T) {
	// Use a fresh tmp dir for the on-disk template (so we don't
	// pick up the bundled constant or any leftover state from
	// other tests). Also reset the global cache so this test
	// starts from a known-empty state.
	tmpDir := t.TempDir()
	t.Setenv("GALLERY_TEMPLATES_DIR", tmpDir)

	// Reset the cache for this test only.
	origCache := globalTemplateCache
	globalTemplateCache = nil
	t.Cleanup(func() { globalTemplateCache = origCache })

	tmplPath := tmpDir + "/gallery.tmpl"
	templateBody := `<html><body>{{.Title}}</body></html>`
	if err := os.WriteFile(tmplPath, []byte(templateBody), 0o644); err != nil {
		t.Fatal(err)
	}

	// First call: cache miss, parses the file.
	tmpl1, err := loadTemplate("")
	if err != nil {
		t.Fatalf("first loadTemplate: %v", err)
	}
	if tmpl1 == nil {
		t.Fatal("first call returned nil template")
	}

	// Sanity check: the template should be cached now.
	cached := getCachedTemplate()
	cached.mu.RLock()
	hasOnDisk := cached.onDisk != nil && cached.onDisk.path == tmplPath
	cached.mu.RUnlock()
	if !hasOnDisk {
		t.Error("expected on-disk cache to be populated after first call")
	}

	// Second call: cache hit (same file, same mtime). The returned
	// *template.Template should be the SAME pointer as the first.
	tmpl2, err := loadTemplate("")
	if err != nil {
		t.Fatalf("second loadTemplate: %v", err)
	}
	if tmpl2 != tmpl1 {
		t.Error("expected cache hit (same *template.Template pointer on second call); got a different pointer")
	}

	// Touch the file (update mtime) and call again. The mtime
	// change should invalidate the cache and force a re-parse.
	// We write a new body to ensure the parse result is also
	// observably different.
	newBody := `<html><body>{{.Title}} UPDATED</body></html>`
	// Sleep to ensure the mtime ticks (filesystem mtime resolution
	// can be coarse on some systems).
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(tmplPath, []byte(newBody), 0o644); err != nil {
		t.Fatal(err)
	}

	tmpl3, err := loadTemplate("")
	if err != nil {
		t.Fatalf("third loadTemplate: %v", err)
	}
	if tmpl3 == tmpl1 {
		t.Error("expected cache miss after mtime change (new *template.Template pointer); got the old one")
	}

	// Verify the new template is functional with the new body.
	var buf bytes.Buffer
	if err := tmpl3.Execute(&buf, map[string]string{"Title": "hello"}); err != nil {
		t.Fatalf("execute on new template: %v", err)
	}
	if !strings.Contains(buf.String(), "UPDATED") {
		t.Errorf("expected new template body in output, got %q", buf.String())
	}

	// Fourth call (no change): cache hit again, same pointer.
	tmpl4, err := loadTemplate("")
	if err != nil {
		t.Fatalf("fourth loadTemplate: %v", err)
	}
	if tmpl4 != tmpl3 {
		t.Error("expected cache hit after no change (same *template.Template pointer); got a different pointer")
	}
}

// TestLoadTemplate_CachesBundledTemplate verifies the bundled
// template is also cached: the first call to loadTemplate for a
// missing on-disk file parses the bundled constant; the second
// call returns the SAME *template.Template pointer.
func TestLoadTemplate_CachesBundledTemplate(t *testing.T) {
	// Point the templates dir at an empty tmp dir so the on-disk
	// file is guaranteed to NOT exist (forcing the bundled fallback).
	tmpDir := t.TempDir()
	t.Setenv("GALLERY_TEMPLATES_DIR", tmpDir)

	origCache := globalTemplateCache
	globalTemplateCache = nil
	t.Cleanup(func() { globalTemplateCache = origCache })

	tmpl1, err := loadTemplate("")
	if err != nil {
		t.Fatalf("first loadTemplate: %v", err)
	}
	if tmpl1 == nil {
		t.Fatal("first call returned nil template")
	}

	// Verify the bundled cache slot is populated.
	cached := getCachedTemplate()
	cached.mu.RLock()
	hasBundled := cached.bundled != nil
	cached.mu.RUnlock()
	if !hasBundled {
		t.Error("expected bundled cache slot to be populated after first call")
	}

	// Second call: cache hit (bundled template never changes).
	tmpl2, err := loadTemplate("")
	if err != nil {
		t.Fatalf("second loadTemplate: %v", err)
	}
	if tmpl2 != tmpl1 {
		t.Error("expected cache hit for bundled template; got different *template.Template pointer")
	}
}

func TestRenderPage_DirectoriesIgnoreSort(t *testing.T) {
	// Directories in a NON-alphabetical order so we can see if
	// sort took effect. Names: zeta, alpha, mu (alphabetical: alpha, mu, zeta)
	files := []FileInfo{
		{Name: "zeta", Kind: KindDir},
		{Name: "photo.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "alpha", Kind: KindDir},
		{Name: "mu", Kind: KindDir},
		{Name: "zebra.txt", ModTime: 200, Size: 200, Kind: KindOther},
	}

	// Try different sort selections — dirs should always be
	// alphabetical in the output.
	sortSelections := []struct{ field, order string }{
		{"name", "asc"},
		{"name", "desc"},
		{"mtime", "asc"},
		{"mtime", "desc"},
		{"size", "asc"},
		{"size", "desc"},
		{"type", "asc"},
	}

	for _, s := range sortSelections {
		t.Run("sort="+s.field+",order="+s.order+": dirs stay alphabetical", func(t *testing.T) {
			html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files,
				url.Values{"sort": []string{s.field}, "order": []string{s.order}}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			// Debug: print the relevant portion of HTML
			// (Phase 71: the heading now wraps the title in a <span>
			// for the flex layout, so we search for "Directories"
			// and "Other" anywhere in the heading rather than the
			// old direct match.
			// Phase 79: the heading now includes a count in
			// parens (e.g. ">Directories (3)<"), so we use a
			// looser match.
			dirsIdx := strings.Index(html, "Directories (")
			othersIdx := strings.Index(html, "Other files (")
			mediaIdx := strings.Index(html, ">Media (")
			if dirsIdx < 0 || mediaIdx < 0 {
				t.Fatalf("could not find sections: dirs=%d others=%d media=%d", dirsIdx, othersIdx, mediaIdx)
			}
			end := othersIdx
			if end < 0 || mediaIdx < end {
				end = mediaIdx
			}
			dirsSection := html[dirsIdx:end]
			// Extract <tr>...</tr> blocks; for each, get the link text.
			var got []string
			idx := 0
			for {
				trStart := strings.Index(dirsSection[idx:], "<tr>")
				if trStart < 0 {
					break
				}
				trStart += idx
				trEnd := strings.Index(dirsSection[trStart:], "</tr>")
				if trEnd < 0 {
					break
				}
				trEnd += trStart
				tr := dirsSection[trStart:trEnd]
				// Extract the link text. The link contains an icon
				// span + the name, e.g.:
				//   <a class="table-link" href="./alpha/">
				//     <span class="chip-icon">📁</span>alpha/
				//   </a>
				// We need to skip past the icon span and the
				// surrounding whitespace to get just "alpha/".
				linkStart := strings.Index(tr, "table-link")
				if linkStart >= 0 {
					// Find the END of the <a> opening tag (the first
					// ">" after "<a ").
					aStart := strings.Index(tr, "<a ")
					if aStart >= 0 {
						gtStart := strings.Index(tr[aStart:], ">")
						if gtStart >= 0 {
							contentStart := aStart + gtStart + 1
							// Skip past the </span> close of the icon span.
							spanEnd := strings.Index(tr[contentStart:], "</span>")
							if spanEnd >= 0 {
								contentStart += spanEnd + len("</span>")
							}
							// Now find the closing </a>.
							gtEnd := strings.Index(tr[contentStart:], "</a>")
							if gtEnd >= 0 {
								linkText := tr[contentStart : contentStart+gtEnd]
								linkText = strings.TrimSpace(linkText)
								linkText = strings.TrimSuffix(linkText, "/")
								if linkText != "" {
									got = append(got, linkText)
								}
							}
						}
					}
				}
				idx = trEnd + 1
			}
			want := []string{"alpha", "mu", "zeta"}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("expected dirs order %v (always alpha), got %v. dirsSection snippet: %q", want, got, dirsSection[:min(800, len(dirsSection))])
			}
		})
	}
}

// TestRenderPage_SectionToggleMarkup verifies the Phase 71
// feature: the directories + other-files sections each have
// a toggle button in the heading that can collapse the body.
// We check the rendered HTML for the new structure:
//   - <section class="dirs-section" data-section="dirs">
//   - <button class="section-toggle" data-toggle="dirs">
//   - <div class="section-body" id="dirs-body">
//
// Same structure for others.
func TestRenderPage_SectionToggleMarkup(t *testing.T) {
	files := []FileInfo{
		{Name: "alpha", Kind: KindDir},
		{Name: "mu", Kind: KindDir},
		{Name: "readme.txt", ModTime: 100, Size: 100, Kind: KindOther},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Dirs section: data-section attr on <section>
	if !strings.Contains(html, `<section class="dirs-section" data-section="dirs">`) {
		t.Error(`expected <section class="dirs-section" data-section="dirs">`)
	}
	// Toggle button with the right data-toggle value
	if !strings.Contains(html, `class="section-toggle" data-toggle="dirs"`) {
		t.Error(`expected toggle button for dirs section`)
	}
	// Initial button text: minus (expanded)
	if !strings.Contains(html, `data-toggle="dirs" aria-expanded="true"`) {
		t.Error(`expected toggle button to start with aria-expanded=true (expanded)`)
	}
	// Section body wrapper
	if !strings.Contains(html, `<div class="section-body" id="dirs-body">`) {
		t.Error(`expected <div class="section-body" id="dirs-body"> wrapper around dirs content`)
	}

	// Others section: same structure
	if !strings.Contains(html, `<section class="others-section" data-section="others">`) {
		t.Error(`expected <section class="others-section" data-section="others">`)
	}
	if !strings.Contains(html, `class="section-toggle" data-toggle="others"`) {
		t.Error(`expected toggle button for others section`)
	}
	if !strings.Contains(html, `data-toggle="others" aria-expanded="true"`) {
		t.Error(`expected toggle button to start with aria-expanded=true (expanded)`)
	}
	if !strings.Contains(html, `<div class="section-body" id="others-body">`) {
		t.Error(`expected <div class="section-body" id="others-body"> wrapper around others content`)
	}
}

// TestBundledTemplate_SectionToggleJSValid verifies the Phase 71
// JS (localStorage + click handler) is included in the
// bundled template. We use the same Execute-into-buffer
// pattern as TestBundledTemplate_LightboxJSValidSyntax,
// then check that the JS contains the expected pieces.
//
// Note: we can't search for a comment marker ("// SECTION
// TOGGLE" or "/* SECTION TOGGLE */") because Go's html/template
// strips comments from <script> blocks during parsing. So we
// search for the actual code identifiers: STORAGE_PREFIX,
// 'gallery-section-' (the localStorage key prefix), and the
// querySelectorAll selector for .section-toggle buttons.
func TestBundledTemplate_SectionToggleJSValid(t *testing.T) {
	tmpl, err := loadTemplate("")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, PageData{Title: "test"}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	// Check for the section toggle JS pieces.
	checks := []struct {
		name   string
		substr string
	}{
		{"localStorage usage", "localStorage"},
		{"addEventListener usage", "addEventListener"},
		{"querySelectorAll usage", "querySelectorAll"},
		{"classList usage", "classList"},
		{"STORAGE_PREFIX identifier", "STORAGE_PREFIX"},
		{"section-toggle selector", "section-toggle"},
		{"data-section selector pattern", `[data-section="`},
		{"gallery-section- prefix", "gallery-section-"},
		{"section-heading click target (Phase 74)", ".section-heading"},
		{"toggleSection function (Phase 74)", "toggleSection"},
	}
	for _, c := range checks {
		if !strings.Contains(html, c.substr) {
			t.Errorf("expected %q (%s) in template", c.substr, c.name)
		}
	}
}

// TestRenderPage_Phase72UIChanges verifies the Phase 72 set
// of UI changes:
//  1. Section heading font-size is 0.85rem (was 0.7rem)
//  2. Heading has a divider span (the horizontal rule between
//     title and toggle button)
//  3. The up-row is the FIRST <tr> in the dirs table (not
//     a separate up-chip-row above the table)
//  4. The dirs table has an up-spacer row after the up-row
//  5. The dirs table has a col-date column with dates populated
//     (per the user's bug report)
//  6. Per Phase 85: the sort-btn.active .arrow now inherits
//     the active button's text color (--active-fg), which
//     means it can be EITHER light or dark depending on the
//     theme. The explicit color: white rule was removed in
//     Phase 85 since the active button is no longer always
//     dark (it inverts the page colors in each mode).
func TestRenderPage_Phase72UIChanges(t *testing.T) {
	// Set up a gallery with a dir + an image, in a subdir
	// (so we have an up-row to render).
	files := []FileInfo{
		{Name: "nested1", Kind: KindDir, ModTime: 100},
		{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	html, err := RenderPage("subdir", "./", "./_thumbs/", "subdir", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Section heading font-size: 0.85rem (was 0.7rem).
	// Per Phase 72: the section-heading CSS rule was updated to
	// use 0.85rem. Other rules (thead, page-ellipsis, filetype-chip)
	// still use 0.7rem, so we check the specific selector.
	if !strings.Contains(html, `.section-heading {`) || !strings.Contains(html, `font-size: 0.85rem`) {
		t.Error("expected .section-heading CSS rule to use font-size: 0.85rem (Phase 72: bumped from 0.7rem)")
	}
	// And the OLD section-heading rule with 0.7rem should be GONE.
	if strings.Contains(html, `.section-heading {`) {
		// Check that the rule doesn't have 0.7rem
		start := strings.Index(html, `.section-heading {`)
		end := strings.Index(html[start:], `}`)
		rule := html[start : start+end]
		if strings.Contains(rule, `font-size: 0.7rem`) {
			t.Errorf("expected the .section-heading rule's font-size to NOT be 0.7rem; rule: %q", rule)
		}
	}

	// 2. Heading has a divider span (between title and toggle)
	// The dirs section heading should have <span class="heading-divider">
	// in addition to the title <span> and the toggle <button>.
	if !strings.Contains(html, `<span class="heading-divider" aria-hidden="true"></span>`) {
		t.Error(`expected <span class="heading-divider"> in dirs section heading (Phase 72)`)
	}
	// Others section should also have the divider.
	othersIdx := strings.Index(html, "Other files")
	if othersIdx < 0 {
		// no others, that's fine — just check dirs
	} else {
		if !strings.Contains(html[othersIdx:],
			`<span class="heading-divider" aria-hidden="true"></span>`) {
			t.Error(`expected <span class="heading-divider"> in others section heading (Phase 72)`)
		}
	}

	// 3. The up-row is in a SEPARATE up-row-table (Phase 76)
	// that lives ABOVE the dirs-table. We just verify the
	// up-row-table exists in the HTML.
	dirsTableStart := strings.Index(html, `<table class="files-table dirs-table">`)
	if dirsTableStart < 0 {
		t.Fatal("no dirs-table found")
	}
	upRowStart := strings.Index(html, `<table class="up-row-table">`)
	if upRowStart < 0 {
		t.Fatal("no up-row-table found")
	}
	// The up-row-table should appear BEFORE the dirs-table
	// (above it in the rendered page).
	if upRowStart > dirsTableStart {
		t.Error("expected up-row-table to be BEFORE the dirs-table (Phase 76: separate table above)")
	}
	// The up-row should be the FIRST <tr> AFTER the <tbody> opening
	// tag (not counting the thead <tr>).
	dirsTableEnd := strings.Index(html[dirsTableStart:], `</table>`)
	dirsTable := html[dirsTableStart : dirsTableStart+dirsTableEnd]
	// Per Phase 76: the up-row is now in a SEPARATE table
	// above the dirs-table. So the dirs-table's <tbody> only
	// has the subdirs (no up-row). We just verify the dirs-
	// table structure is correct (thead + tbody with subdirs).
	tbodyStart := strings.Index(html[dirsTableStart:], `<tbody>`)
	if tbodyStart < 0 {
		t.Fatal("no <tbody> in dirs-table")
	}
	tbodyStart += dirsTableStart
	// Find the first <tr...> AFTER <tbody> — should be the
	// FIRST SUBDIR, not an up-row.
	trAfterTbody := strings.Index(html[tbodyStart:], `<tr`)
	if trAfterTbody < 0 {
		t.Fatal("no <tr> in tbody")
	}
	trAfterTbody += tbodyStart
	// The first <tr> after <tbody> should NOT be the up-row
	// (the up-row is in a separate table above).
	if strings.Contains(html[trAfterTbody:trAfterTbody+100], `class="up-row"`) {
		t.Error("expected the first <tr> in dirs-table tbody to NOT be an up-row (Phase 76)")
	}

	// 4. The dirs table does NOT have an up-spacer row anymore
	// (Phase 76: the up-row is in a separate up-row-table,
	// the dirs-table only has the subdirs).
	if strings.Contains(dirsTable, `class="up-spacer"`) {
		t.Error("expected NO up-spacer row in dirs-table (Phase 76: replaced by separate up-row-table)")
	}

	// 5. The dirs table has a col-date column with dates populated.
	// Per Phase 72: the test file's ModTime=100 should render as a
	// non-empty date string.
	if !strings.Contains(dirsTable, `<td class="col-date">`) {
		t.Error(`expected <td class="col-date"> column in dirs-table`)
	}
	// The date should NOT be empty (was empty when ModTime=0).
	// Check that at least one <td class="col-date"> has non-empty content.
	dateCells := strings.Count(dirsTable, `<td class="col-date">`)
	emptyDates := strings.Count(dirsTable, `<td class="col-date"></td>`)
	if dateCells == emptyDates {
		t.Errorf("expected some populated col-date cells; got %d date cells, %d empty", dateCells, emptyDates)
	}

	// 6. Per Phase 85: the .sort-btn.active .arrow no longer
	// has an explicit color: white rule. The arrow inherits
	// the active button's text color (--active-fg) which is
	// themed. We verify the rule was removed.
	if strings.Contains(html, `.sort-btn.active .arrow { color: white; }`) {
		t.Error(`expected .sort-btn.active .arrow to NOT have color: white (Phase 85: removed — arrow inherits --active-fg)`)
	}
	// And the old hard-coded blue should be gone.
	if strings.Contains(html, `.sort-btn .arrow { margin-left: 0.2rem; font-weight: 600; color: #006ed3; }`) {
		t.Error(`expected the old .sort-btn .arrow rule with #006ed3 to be GONE (replaced by Phase 72 white arrow)`)
	}
}

// TestRenderPage_TableRowClickable verifies Phase 73: the
// complete table row for directories and other files is
// clickable (not just the Name cell). The Type/Size/Date
// cells each wrap their content in a <a class="cell-link">
// with the same href as the Name cell, so clicking anywhere
// in the row navigates.
func TestRenderPage_TableRowClickable(t *testing.T) {
	files := []FileInfo{
		{Name: "alpha", Kind: KindDir, ModTime: 100},
		{Name: "readme.txt", ModTime: 200, Size: 2048, Kind: KindOther},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Find the dirs table.
	dirsStart := strings.Index(html, `<table class="files-table dirs-table">`)
	if dirsStart < 0 {
		t.Fatal("no dirs-table")
	}
	dirsEnd := strings.Index(html[dirsStart:], `</table>`) + dirsStart
	dirsTable := html[dirsStart:dirsEnd]

	// Per Phase 77: the dirs table no longer has a Type column
	// (all entries are DIR, so the column was redundant). The
	// dirs row (for "alpha") should have cell-link anchors in
	// the Date cell only (not the Name cell, not the removed
	// Type cell). We back-search for the <tr> to capture the
	// Name link's href (which is BEFORE the "alpha/" text).
	alphaIdx := strings.Index(dirsTable, "alpha/")
	if alphaIdx < 0 {
		t.Fatal("no alpha row in dirs-table")
	}
	alphaTrStart := strings.LastIndex(dirsTable[:alphaIdx], "<tr>")
	alphaTrStart2 := strings.LastIndex(dirsTable[:alphaIdx], "<tr ")
	alphaRowStart := alphaTrStart
	if alphaTrStart2 > alphaRowStart {
		alphaRowStart = alphaTrStart2
	}
	alphaRowEnd := strings.Index(dirsTable[alphaRowStart:], "</tr>") + alphaRowStart
	alphaRow := dirsTable[alphaRowStart:alphaRowEnd]

	// Count cell-link occurrences in the alpha row.
	cellLinks := strings.Count(alphaRow, `class="table-link cell-link"`)
	if cellLinks != 1 {
		t.Errorf("expected 1 cell-link in the alpha row (Date column only, after Phase 77 removed Type), got %d in row: %q", cellLinks, alphaRow)
	}
	// All anchors should have the same href (./alpha/).
	hrefCount := strings.Count(alphaRow, `href="./alpha/"`)
	if hrefCount != 2 {
		t.Errorf("expected 2 anchors with href=./alpha/ (Name + 1 cell-link), got %d", hrefCount)
	}

	// Now check the others table.
	othersStart := strings.Index(html, `<table class="files-table others-table">`)
	if othersStart < 0 {
		t.Fatal("no others-table")
	}
	othersEnd := strings.Index(html[othersStart:], `</table>`) + othersStart
	othersTable := html[othersStart:othersEnd]

	readmeRowStart := strings.Index(othersTable, "readme.txt")
	if readmeRowStart < 0 {
		t.Fatal("no readme row in others-table")
	}
	readmeTrStart := strings.LastIndex(othersTable[:readmeRowStart], "<tr>")
	readmeTrStart2 := strings.LastIndex(othersTable[:readmeRowStart], "<tr ")
	readmeStart := readmeTrStart
	if readmeTrStart2 > readmeStart {
		readmeStart = readmeTrStart2
	}
	readmeRowEnd := strings.Index(othersTable[readmeStart:], "</tr>") + readmeStart
	readmeRow := othersTable[readmeStart:readmeRowEnd]

	// The readme.txt row should have 3 cell-links (Type + Size + Date).
	cellLinks = strings.Count(readmeRow, `class="table-link cell-link"`)
	if cellLinks != 3 {
		t.Errorf("expected 3 cell-links in the readme.txt row (Type + Size + Date), got %d in row: %q", cellLinks, readmeRow)
	}
	// All 4 anchors (Name + 3 cell-links) should have the same href.
	hrefCount = strings.Count(readmeRow, `href="./readme.txt"`)
	if hrefCount != 4 {
		t.Errorf("expected 4 anchors with href=./readme.txt, got %d", hrefCount)
	}

	// The cell-links should have tabindex="-1" (keyboard
	// navigation goes to the Name link only, not all 4 per row).
	if !strings.Contains(alphaRow, `tabindex="-1"`) {
		t.Error(`expected cell-link to have tabindex="-1" (so keyboard tab goes to Name only)`)
	}

	// The cell-links should have aria-hidden="true" (screen
	// readers announce only the Name link, not all 4).
	if !strings.Contains(alphaRow, `aria-hidden="true"`) {
		t.Error(`expected cell-link to have aria-hidden="true" (so SR announces Name only)`)
	}
}

// TestRenderPage_SectionHeadingClickable verifies Phase 74:
// the whole section heading row is clickable to toggle
// show/hide, not just the small − / + button. The check is
// structural (the JS contains the right click handler) plus
// CSS (cursor: pointer on the heading + a hover state).
func TestRenderPage_SectionHeadingClickable(t *testing.T) {
	files := []FileInfo{
		{Name: "alpha", Kind: KindDir, ModTime: 100},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 1. CSS: cursor: pointer on .section-heading (the whole
	// heading is clickable, not just the button).
	if !strings.Contains(html, `.section-heading {`) {
		t.Fatal("no .section-heading rule found")
	}
	start := strings.Index(html, `.section-heading {`)
	end := strings.Index(html[start:], `}`)
	rule := html[start : start+end]
	if !strings.Contains(rule, `cursor: pointer`) {
		t.Errorf("expected .section-heading to have cursor: pointer (Phase 74); rule: %q", rule)
	}
	// 2. CSS: hover state on the heading (the bg-hover token).
	if !strings.Contains(html, `.section-heading:hover`) {
		t.Error("expected a .section-heading:hover CSS rule (Phase 74: full-width click affordance)")
	}

	// 3. JS: the click handler is attached to .section-heading
	// (not just to .section-toggle).
	if !strings.Contains(html, `var headings = document.querySelectorAll('.dirs-section .section-heading, .others-section .section-heading')`) {
		t.Error("expected the JS to find .section-heading (Phase 74: full-width click target)")
	}
	// 4. JS: a click handler is added to each heading.
	if !strings.Contains(html, `h.addEventListener('click', function()`) {
		t.Error("expected the JS to addEventListener('click', ...) on each .section-heading (Phase 74)")
	}
	// 5. JS: the button still has its own click handler (for
	// keyboard / screen reader users who tab to the button).
	if !strings.Contains(html, `btn.addEventListener('click', function(e)`) {
		t.Error("expected the JS to keep the button's click handler (for keyboard a11y)")
	}
	// 6. JS: stopPropagation on the button click (so it doesn't
	// trigger the heading handler twice when the button is clicked).
	if !strings.Contains(html, `e.stopPropagation()`) {
		t.Error("expected the button's click handler to call e.stopPropagation (Phase 74: avoid double-toggle)")
	}
}

// TestRenderPage_Phase75HorizontalLinesSameWidth verifies
// Phase 75: the three horizontal lines (under the sort-bar,
// under each section, and the header's bottom) are all the
// same width. The line under the sort-bar is now drawn by
// .sort-bar's border-bottom (with negative margin to escape
// the header's 2rem padding), so it extends to the viewport
// edges like the .section border-bottom.
//
// The <header>'s border-bottom has been REMOVED (it used to
// draw a line at the <header>'s outer edge, which was slightly
// different width than the section lines because the section
// has its own padding).
func TestRenderPage_Phase75HorizontalLinesSameWidth(t *testing.T) {
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 1. The <header> rule should NOT have border-bottom anymore.
	headerStart := strings.Index(html, "header {")
	if headerStart < 0 {
		t.Fatal("no header rule")
	}
	// Find the matching closing brace (the next "}" — header is
	// a simple 1-property rule so the closing brace is near).
	headerEnd := strings.Index(html[headerStart:], "}")
	if headerEnd < 0 {
		t.Fatal("no end of header rule")
	}
	headerRule := html[headerStart : headerStart+headerEnd+1]
	if strings.Contains(headerRule, "border-bottom") {
		t.Errorf("expected <header> rule to NOT have border-bottom (Phase 75: moved to sort-bar); rule: %q", headerRule)
	}

	// 1b. Per Phase 80: the .header-top rule should have
	// border-bottom + padding-bottom (the new visual separator
	// between the title/meta row and the sort-bar).
	headerTopStart := strings.Index(html, ".header-top {")
	if headerTopStart < 0 {
		t.Fatal("no .header-top rule")
	}
	headerTopEnd := strings.Index(html[headerTopStart:], "}")
	if headerTopEnd < 0 {
		t.Fatal("no end of .header-top rule")
	}
	headerTopRule := html[headerTopStart : headerTopStart+headerTopEnd+1]
	if !strings.Contains(headerTopRule, "border-bottom: 1px solid var(--border)") {
		t.Errorf("expected .header-top to have border-bottom (Phase 80); rule: %q", headerTopRule)
	}
	if !strings.Contains(headerTopRule, "padding-bottom: 0.75rem") {
		t.Errorf("expected .header-top to have padding-bottom: 0.75rem (Phase 80); rule: %q", headerTopRule)
	}
	// And should NOT have margin-bottom: 0.85rem (removed in Phase 80).
	if strings.Contains(headerTopRule, "margin-bottom: 0.85rem") {
		t.Errorf("expected .header-top to NOT have margin-bottom: 0.85rem (Phase 80: removed); rule: %q", headerTopRule)
	}

	// 2. The .sort-bar rule should have border-bottom (not border-top).
	sortBarStart := strings.Index(html, ".sort-bar {")
	if sortBarStart < 0 {
		t.Fatal("no .sort-bar rule")
	}
	sortBarEnd := strings.Index(html[sortBarStart:], "}")
	if sortBarEnd < 0 {
		t.Fatal("no end of .sort-bar rule")
	}
	sortBarRule := html[sortBarStart : sortBarStart+sortBarEnd+1]
	if !strings.Contains(sortBarRule, "border-bottom") {
		t.Errorf("expected .sort-bar rule to have border-bottom (Phase 75); rule: %q", sortBarRule)
	}
	if strings.Contains(sortBarRule, "border-top") {
		t.Errorf("expected .sort-bar rule to NOT have border-top (Phase 75: moved to border-bottom); rule: %q", sortBarRule)
	}
	// The negative margin is what makes the line extend to the
	// viewport edges (escapes the <header>'s 2rem padding).
	// Per Phase 80: the sort-bar no longer has negative
	// horizontal margin (it was margin: 0 -2rem to escape the
	// header's 2rem padding; the user removed it in Phase 80).
	if strings.Contains(sortBarRule, "margin: 0 -2rem") {
		t.Errorf("expected .sort-bar to NOT have margin: 0 -2rem (Phase 80: removed)")
	}
	// And the new padding should be 0.75rem 0 0.75rem 0.
	if !strings.Contains(sortBarRule, "padding: 0.75rem 0 0.75rem 0") {
		t.Errorf("expected .sort-bar to have padding: 0.75rem 0 0.75rem 0 (Phase 80); rule: %q", sortBarRule)
	}

	// 3. The .section rule should have border-bottom (unchanged).
	sectionStart := strings.Index(html, ".section {")
	if sectionStart < 0 {
		t.Fatal("no .section rule")
	}
	sectionEnd := strings.Index(html[sectionStart:], "}")
	if sectionEnd < 0 {
		t.Fatal("no end of .section rule")
	}
	sectionRule := html[sectionStart : sectionStart+sectionEnd+1]
	if !strings.Contains(sectionRule, "border-bottom") {
		t.Errorf("expected .section rule to have border-bottom (unchanged from Phase 75); rule: %q", sectionRule)
	}
}

// TestRenderPage_Phase76UpRowAsSeparateTable verifies Phase 76:
// the Up entry is in a separate <table class="up-row-table">
// above the dirs table. The up-spacer row is GONE (it used to
// highlight on hover). The dirs-table only contains the
// subdirs, not the up entry.
func TestRenderPage_Phase76UpRowAsSeparateTable(t *testing.T) {
	files := []FileInfo{
		{Name: "nested1", Kind: KindDir, ModTime: 100},
		{Name: "nested2", Kind: KindDir, ModTime: 200},
	}
	html, err := RenderPage("subdir", "./", "./_thumbs/", "subdir", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 1. The <table class="up-row-table"> should be present.
	upRowTableStart := strings.Index(html, `<table class="up-row-table">`)
	if upRowTableStart < 0 {
		t.Fatal(`expected <table class="up-row-table"> (Phase 76: up entry in separate table)`)
	}

	// 2. The dirs-table should be present.
	dirsTableStart := strings.Index(html, `<table class="files-table dirs-table">`)
	if dirsTableStart < 0 {
		t.Fatal(`expected <table class="files-table dirs-table"> (Phase 76: dirs table still has subdirs)`)
	}

	// 3. The up-row-table should appear BEFORE the dirs-table.
	if upRowTableStart > dirsTableStart {
		t.Errorf("expected up-row-table BEFORE dirs-table (got upRowTableStart=%d, dirsTableStart=%d)", upRowTableStart, dirsTableStart)
	}

	// 4. The up-spacer row should NOT be in the HTML at all.
	if strings.Contains(html, `class="up-spacer"`) {
		t.Error("expected NO up-spacer row in HTML (Phase 76: replaced by separate up-row-table)")
	}

	// 5. The dirs-table should NOT contain href="../" (the up
	// entry's href is in the up-row-table, not the dirs-table).
	dirsTableEnd := strings.Index(html[dirsTableStart:], `</table>`) + dirsTableStart
	dirsTable := html[dirsTableStart:dirsTableEnd]
	if strings.Contains(dirsTable, `href="../"`) {
		t.Error(`expected NO href="../" in dirs-table (Phase 76: up entry is in separate up-row-table)`)
	}

	// 6. The up-row-table SHOULD contain href="../" and the up text.
	upRowTableEnd := strings.Index(html[upRowTableStart:], `</table>`) + upRowTableStart
	upRowTable := html[upRowTableStart:upRowTableEnd]
	if !strings.Contains(upRowTable, `href="../"`) {
		t.Error(`expected up-row-table to contain the up entry's href="../"`)
	}
	if !strings.Contains(upRowTable, `Up (`) {
		t.Error("expected up-row-table to contain the 'Up (...)' text")
	}
	// The up-row-table should have NO <thead> (just a tbody
	// with one row, no column headers since the row spans all
	// 3 columns).
	if strings.Contains(upRowTable, `<thead>`) {
		t.Error("expected up-row-table to NOT have a <thead> (it has no column headers)")
	}

	// 7. CSS: the .up-row-table rule should be present.
	if !strings.Contains(html, `.up-row-table {`) {
		t.Error("expected .up-row-table CSS rule (Phase 76)")
	}
	// 8. CSS: the OLD .dirs-table .up-row rule should be GONE.
	if strings.Contains(html, `.dirs-table .up-row td {`) {
		t.Error("expected the OLD .dirs-table .up-row CSS rule to be GONE (Phase 76)")
	}
	// 9. CSS: the OLD .dirs-table .up-spacer rule should be GONE.
	if strings.Contains(html, `.dirs-table .up-spacer td {`) {
		t.Error("expected the OLD .dirs-table .up-spacer CSS rule to be GONE (Phase 76)")
	}
}

// TestRenderPage_Phase77DirsTableNoTypeColumn verifies Phase 77:
// the dirs table no longer has a Type column (since all
// entries are DIR, the column was redundant). The dirs
// table now has only Name and Modified columns.
func TestRenderPage_Phase77DirsTableNoTypeColumn(t *testing.T) {
	files := []FileInfo{
		{Name: "alpha", Kind: KindDir, ModTime: 100},
		{Name: "beta", Kind: KindDir, ModTime: 200},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Find the dirs table.
	dirsStart := strings.Index(html, `<table class="files-table dirs-table">`)
	if dirsStart < 0 {
		t.Fatal("no dirs-table")
	}
	dirsEnd := strings.Index(html[dirsStart:], `</table>`) + dirsStart
	dirsTable := html[dirsStart:dirsEnd]

	// 1. The dirs table's thead should have ONLY Name + Modified
	// (no Type column). Count the <th> elements.
	thCount := strings.Count(dirsTable, `<th class="col-`)
	if thCount != 2 {
		t.Errorf("expected 2 <th> elements in dirs-table thead (Name + Modified), got %d in: %q", thCount, dirsTable)
	}
	// 2. The thead should NOT have a col-type <th>.
	if strings.Contains(dirsTable, `<th class="col-type">Type</th>`) {
		t.Error("expected NO <th class=\"col-type\">Type</th> in dirs-table (Phase 77: Type column removed)")
	}
	// 3. The Name column should still be there.
	if !strings.Contains(dirsTable, `<th class="col-name">Name</th>`) {
		t.Error("expected <th class=\"col-name\">Name</th> in dirs-table")
	}
	// 4. The Modified column should still be there.
	if !strings.Contains(dirsTable, `<th class="col-date">Modified</th>`) {
		t.Error("expected <th class=\"col-date\">Modified</th> in dirs-table")
	}

	// 5. The dirs-table body should have NO <td class="col-type"> cells.
	colTypeCells := strings.Count(dirsTable, `<td class="col-type">`)
	if colTypeCells != 0 {
		t.Errorf("expected NO <td class=\"col-type\"> cells in dirs-table, got %d", colTypeCells)
	}

	// 6. The up-row-table's td should have colspan="2" (was 3).
	// We need a subdir context to have an up-row-table.
	// (Re-render with a relPath to enable the up entry.)
	upHTML, err := RenderPage("test", "./", "./_thumbs/", "subdir", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	upRowTableStart := strings.Index(upHTML, `<table class="up-row-table">`)
	if upRowTableStart < 0 {
		t.Fatal("no up-row-table in subdir context")
	}
	upRowTableEnd := strings.Index(upHTML[upRowTableStart:], `</table>`) + upRowTableStart
	upRowTable := upHTML[upRowTableStart:upRowTableEnd]
	if !strings.Contains(upRowTable, `colspan="2"`) {
		t.Error(`expected colspan="2" in up-row-table td (Phase 77: matches the new 2-column dirs table)`)
	}
	if strings.Contains(upRowTable, `colspan="3"`) {
		t.Error(`expected NO colspan="3" in up-row-table td (Phase 77: was 3 columns, now 2)`)
	}
}

// TestRenderPage_TotalFilesInMetaLine verifies Phase 78:
// the meta line now shows the total number of files at the
// start (as "N files" or "1 file"), followed by the breakdown
// (images / videos / other files). The total is the sum of
// ImageCount + TotalVideos + len(OtherFiles).
func TestRenderPage_TotalFilesInMetaLine(t *testing.T) {
	files := []FileInfo{
		{Name: "img1.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "img2.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "img3.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "vid1.mp4", ModTime: 1, Size: 100, Kind: KindVideo},
		{Name: "readme.txt", ModTime: 1, Size: 100, Kind: KindOther},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Find the meta line. The total is the first <span> after
	// <div class="meta">.
	metaStart := strings.Index(html, `<div class="meta">`)
	if metaStart < 0 {
		t.Fatal("no meta div")
	}
	metaEnd := strings.Index(html[metaStart:], `</div>`) + metaStart
	meta := html[metaStart:metaEnd]

	// 1. The total "5 files" should be the first <span> in the
	// meta line (3 images + 1 video + 1 other = 5 files).
	firstSpan := strings.Index(meta, `<span>`)
	if firstSpan < 0 {
		t.Fatal("no first <span> in meta")
	}
	// Extract the first span's text.
	gtIdx := strings.Index(meta[firstSpan:], `>`)
	ltIdx := strings.Index(meta[firstSpan+gtIdx:], `<`)
	if gtIdx < 0 || ltIdx < 0 {
		t.Fatal("malformed first span")
	}
	firstSpanText := meta[firstSpan+gtIdx+1 : firstSpan+gtIdx+ltIdx]
	if firstSpanText != `5 files` {
		t.Errorf("expected first span to be '5 files', got %q", firstSpanText)
	}

	// 2. The "3 images" should come next.
	if !strings.Contains(meta, `<span>3 images</span>`) {
		t.Error("expected '<span>3 images</span>' in meta line")
	}
	// 3. The "1 videos" (videos is grammatically a bit off but
	// matches the existing style).
	if !strings.Contains(meta, `<span>1 videos</span>`) {
		t.Error("expected '<span>1 videos</span>' in meta line")
	}
	// 4. The "1 other files" (other files is plural-only even for 1).
	if !strings.Contains(meta, `<span>1 other files</span>`) {
		t.Error("expected '<span>1 other files</span>' in meta line")
	}

	// 5. With NO files, the meta line should show "0 files"
	// (plural form for 0).
	noFiles, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	noMetaStart := strings.Index(noFiles, `<div class="meta">`)
	noMetaEnd := strings.Index(noFiles[noMetaStart:], `</div>`) + noMetaStart
	noMeta := noFiles[noMetaStart:noMetaEnd]
	noFirstSpan := strings.Index(noMeta, `<span>`)
	noGt := strings.Index(noMeta[noFirstSpan:], `>`)
	noLt := strings.Index(noMeta[noFirstSpan+noGt:], `<`)
	noFirstText := noMeta[noFirstSpan+noGt+1 : noFirstSpan+noGt+noLt]
	if noFirstText != `0 files` {
		t.Errorf("expected '0 files' for empty dir, got %q", noFirstText)
	}

	// 6. With EXACTLY 1 file, the meta line should show "1 file"
	// (singular form).
	oneFile := []FileInfo{
		{Name: "only.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	oneHTML, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, oneFile, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	oneMetaStart := strings.Index(oneHTML, `<div class="meta">`)
	oneMetaEnd := strings.Index(oneHTML[oneMetaStart:], `</div>`) + oneMetaStart
	oneMeta := oneHTML[oneMetaStart:oneMetaEnd]
	oneFirstSpan := strings.Index(oneMeta, `<span>`)
	oneGt := strings.Index(oneMeta[oneFirstSpan:], `>`)
	oneLt := strings.Index(oneMeta[oneFirstSpan+oneGt:], `<`)
	oneFirstText := oneMeta[oneFirstSpan+oneGt+1 : oneFirstSpan+oneGt+oneLt]
	if oneFirstText != `1 file` {
		t.Errorf("expected '1 file' (singular) for 1 file, got %q", oneFirstText)
	}
}

// TestRenderPage_Phase79HeadingCounts verifies Phase 79:
// the section headings now show a count in parens after
// the title, e.g. "Directories (3)" and "Other files (2)".
// The count is the number of entries in that section.
func TestRenderPage_Phase79HeadingCounts(t *testing.T) {
	// Set up a subdir context so we have a dirs section.
	files := []FileInfo{
		{Name: "alpha", Kind: KindDir, ModTime: 100},
		{Name: "beta", Kind: KindDir, ModTime: 200},
		{Name: "gamma", Kind: KindDir, ModTime: 300},
		{Name: "img1.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "readme.txt", ModTime: 1, Size: 100, Kind: KindOther},
		{Name: "notes.md", ModTime: 1, Size: 100, Kind: KindOther},
	}
	html, err := RenderPage("subdir", "./", "./_thumbs/", "subdir", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 1. The dirs heading should show "Directories (3)".
	if !strings.Contains(html, "Directories (3)") {
		t.Error("expected dirs heading to be 'Directories (3)' (Phase 79)")
	}
	// 2. The others heading should show "Other files (2)".
	if !strings.Contains(html, "Other files (2)") {
		t.Error("expected others heading to be 'Other files (2)' (Phase 79)")
	}

	// 3. With no dirs (gallery root, no up), no dirs heading.
	rootHTML, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The dirs section only renders if there's an Up entry or
	// subdirs. With relPath="" and 3 subdirs, it should still
	// render at the gallery root... but the test was wrong. Let
	// me think: at the gallery root, "Up" is nil, but subdirs
	// exist, so the section renders. So rootHTML DOES have the
	// dirs heading.
	if !strings.Contains(rootHTML, "Directories (3)") {
		t.Error("expected dirs heading in gallery root too (3 subdirs, no Up)")
	}

	// 4. With NO subdirs but an Up (deeper subdir with no children),
	// the dirs section should render with count (0).
	deepFiles := []FileInfo{}
	deepHTML, err := RenderPage("deep", "./", "./_thumbs/", "deep", "", false, false, 0, deepFiles, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The dirs section renders if Up is set OR if there are
	// subdirs. At "deep" with no subdirs, Up is non-nil (we're
	// in a subdir), so the section renders with count (0).
	if !strings.Contains(deepHTML, "Directories (0)") {
		t.Error("expected dirs heading 'Directories (0)' when no subdirs but Up entry exists (Phase 79)")
	}
}

// TestRenderPage_Phase82BiggerCloseIcon verifies Phase 82:
// the lightbox close button uses a bigger glyph (✕ U+2715
// MULTIPLICATION X) and a larger font-size so it visually
// balances with the open arrow (↗).
func TestRenderPage_Phase82BiggerCloseIcon(t *testing.T) {
	files := []FileInfo{
		{Name: "img1.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 1. The close button should use the ✕ glyph (U+2715), not
	// the smaller × (U+00D7). The glyph is directly in the
	// button (Phase 91 reverted the wrapping in <span
	// class="lb-btn-icon"> from Phase 88).
	if !strings.Contains(html, "✕</button>") {
		t.Error("expected close button to use ✕ glyph (U+2715) for a bigger close icon (Phase 82)")
	}
	if strings.Contains(html, "×</button>") {
		t.Error("expected close button to NOT use the smaller × glyph (U+00D7) — should be ✕ (U+2715)")
	}

	// 2. The .lb-close CSS should have a larger font-size
	// (1.4rem) than the default (.lb-btn is 1.1rem).
	if !strings.Contains(html, ".lb-close {") {
		t.Fatal("no .lb-close CSS rule")
	}
	start := strings.Index(html, ".lb-close {")
	// Find the .lb-close rule specifically (in the lb-controls
	// context, not the original .lb-close top-right rule).
	// Look for the second occurrence (the lb-controls one).
	start2 := strings.Index(html[start+1:], ".lb-close {")
	if start2 < 0 {
		t.Fatal("only one .lb-close rule (expected 2: top-right + lb-controls)")
	}
	start2 += start + 1
	end := strings.Index(html[start2:], "}")
	if end < 0 {
		t.Fatal("no end of .lb-close rule")
	}
	lbControlsCloseRule := html[start2 : start2+end+1]
	if !strings.Contains(lbControlsCloseRule, "font-size: 1.4rem") {
		t.Errorf("expected .lb-close in lb-controls to have font-size: 1.4rem (Phase 82, .lb-btn-icon selector was removed in Phase 91 revert so it's now directly on .lb-close); rule: %q", lbControlsCloseRule)
	}
}

// TestRenderPage_Phase83UpRowSameFontWeight verifies Phase 83:
// the Up directory link has the same text size as the other
// directory rows (the up-row-table td no longer has
// font-weight: 500, so it inherits the default font-weight
// from the .files-table base, matching the other rows).
func TestRenderPage_Phase83UpRowSameFontWeight(t *testing.T) {
	files := []FileInfo{
		{Name: "nested1", Kind: KindDir, ModTime: 100},
		{Name: "nested2", Kind: KindDir, ModTime: 200},
	}
	html, err := RenderPage("subdir", "./", "./_thumbs/", "subdir", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The .up-row-table td rule should NOT have font-weight: 500
	// (it was removed in Phase 83 so the Up link is the same
	// size as the other directory rows).
	upRuleStart := strings.Index(html, ".up-row-table td {")
	if upRuleStart < 0 {
		t.Fatal("no .up-row-table td rule")
	}
	upRuleEnd := strings.Index(html[upRuleStart:], "}")
	if upRuleEnd < 0 {
		t.Fatal("no end of .up-row-table td rule")
	}
	upRule := html[upRuleStart : upRuleStart+upRuleEnd+1]
	if strings.Contains(upRule, "font-weight: 500") {
		t.Errorf("expected .up-row-table td to NOT have font-weight: 500 (Phase 83: removed for same size as other dirs); rule: %q", upRule)
	}
	if strings.Contains(upRule, "font-weight:") {
		t.Errorf("expected .up-row-table td to NOT have any font-weight (Phase 83: inherit from base); rule: %q", upRule)
	}
}

// TestRenderPage_Phase84UpRowFontSize verifies Phase 84:
// the up-row-table now has font-size: 0.85rem (matching
// .files-table), so the Up link text is the same size as
// the other directory rows in the dirs table below.
func TestRenderPage_Phase84UpRowFontSize(t *testing.T) {
	files := []FileInfo{
		{Name: "nested1", Kind: KindDir, ModTime: 100},
	}
	html, err := RenderPage("subdir", "./", "./_thumbs/", "subdir", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The .up-row-table rule should have font-size: 0.85rem
	// (matching .files-table).
	upRuleStart := strings.Index(html, ".up-row-table {")
	if upRuleStart < 0 {
		t.Fatal("no .up-row-table rule")
	}
	upRuleEnd := strings.Index(html[upRuleStart:], "}")
	if upRuleEnd < 0 {
		t.Fatal("no end of .up-row-table rule")
	}
	upRule := html[upRuleStart : upRuleStart+upRuleEnd+1]
	if !strings.Contains(upRule, "font-size: 0.85rem") {
		t.Errorf("expected .up-row-table to have font-size: 0.85rem (Phase 84); rule: %q", upRule)
	}
}

// TestRenderPage_Phase85ActiveButtonInversion verifies Phase 85:
// the active sort + pagination buttons no longer use the
// blue --accent-bg. They use --active-bg / --active-fg /
// --active-border which are the OPPOSITE mode's page colors
// (a color-contrast inversion: dark active button on light
// page, light active button on dark page).
func TestRenderPage_Phase85ActiveButtonInversion(t *testing.T) {
	files := []FileInfo{
		{Name: "img1.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 1. The .sort-btn.active rule should use --active-bg, NOT
	// --accent-bg. (The old blue accent is gone.)
	if !strings.Contains(html, `.sort-btn.active {`) {
		t.Fatal("no .sort-btn.active rule")
	}
	start := strings.Index(html, `.sort-btn.active {`)
	end := strings.Index(html[start:], `}`)
	rule := html[start : start+end+1]
	if strings.Contains(rule, "var(--accent-bg)") {
		t.Errorf("expected .sort-btn.active to NOT use var(--accent-bg) (Phase 85: replaced with --active-bg); rule: %q", rule)
	}
	if !strings.Contains(rule, "var(--active-bg)") {
		t.Errorf("expected .sort-btn.active to use var(--active-bg) (Phase 85); rule: %q", rule)
	}
	if !strings.Contains(rule, "var(--active-fg)") {
		t.Errorf("expected .sort-btn.active to use var(--active-fg) (Phase 85); rule: %q", rule)
	}

	// 2. The .page-btn.active rule should also use --active-bg.
	if !strings.Contains(html, `.page-btn.active {`) {
		t.Fatal("no .page-btn.active rule")
	}
	start = strings.Index(html, `.page-btn.active {`)
	end = strings.Index(html[start:], `}`)
	pageRule := html[start : start+end+1]
	if strings.Contains(pageRule, "var(--accent-bg)") {
		t.Errorf("expected .page-btn.active to NOT use var(--accent-bg) (Phase 85); rule: %q", pageRule)
	}
	if !strings.Contains(pageRule, "var(--active-bg)") {
		t.Errorf("expected .page-btn.active to use var(--active-bg) (Phase 85); rule: %q", pageRule)
	}

	// 3. The new color tokens should be defined in :root with
	// the dark mode values (the LIGHT mode default — the active
	// button in light mode uses the dark mode's bg).
	if !strings.Contains(html, "--active-bg: #1a1a1a") {
		t.Error("expected --active-bg token defined as #1a1a1a (dark mode's --bg, used by light mode active button)")
	}
	if !strings.Contains(html, "--active-fg: #e5e5e5") {
		t.Error("expected --active-fg token defined as #e5e5e5 (dark mode's --fg, used by light mode active button)")
	}

	// 4. The dark mode override should set --active-bg to the
	// LIGHT mode's value (color inversion).
	// The dark mode blocks contain "--active-bg: #f3f6f7" —
	// the opposite of the default.
	darkActiveBgCount := strings.Count(html, "--active-bg: #f3f6f7")
	if darkActiveBgCount != 2 {
		// Should appear in BOTH the @media (prefers-color-scheme: dark)
		// block AND the [data-theme="dark"] block (= 2 places).
		t.Errorf("expected --active-bg: #f3f6f7 to appear 2 times (both dark mode blocks), got %d", darkActiveBgCount)
	}
}

// TestRenderPage_Phase86LightboxButtonLabels: REMOVED in
// Phase 91 (revert of Phase 86 + 88). The text labels under
// the open/close buttons were removed per user request. The
// buttons are back to the simple Phase 82 state: just ↗ and
// ✕ glyphs, no text labels.

// TestRenderPage_Phase88LabelInsideButton: REMOVED in
// Phase 91 (revert of Phase 86 + 88). The text labels inside
// the button (in a separate span) were removed per user
// request. The buttons are back to the simple Phase 82 state.

// TestRenderPage_Phase91LightboxRevertedLabels verifies the
// revert: the lightbox buttons are simple ↗ and ✕ glyphs, no
// text labels, and the buttons are 28x28 transparent squares
// inside the lb-controls pill (which has the grey rounded
// background).
func TestRenderPage_Phase91LightboxRevertedLabels(t *testing.T) {
	files := []FileInfo{
		{Name: "img1.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 1. The buttons have just the glyph (no <span class="lb-btn-icon">
	// wrapper, no <span class="lb-btn-label">).
	if !strings.Contains(html, `class="lb-btn lb-open"`) {
		t.Error(`expected the open button to exist (Phase 91)`)
	}
	if !strings.Contains(html, `class="lb-btn lb-close"`) {
		t.Error(`expected the close button to exist (Phase 91)`)
	}
	// 2. The buttons should NOT have the <span class="lb-btn-label">
	// wrapper (reverted in Phase 91).
	if strings.Contains(html, `<span class="lb-btn-label">`) {
		t.Error("expected NO <span class=\"lb-btn-label\"> (Phase 91: labels removed)")
	}
	// 3. The buttons should NOT have the <span class="lb-btn-icon">
	// wrapper either (reverted in Phase 91).
	if strings.Contains(html, `<span class="lb-btn-icon">`) {
		t.Error("expected NO <span class=\"lb-btn-icon\"> (Phase 91: reverted, icon is directly in button)")
	}
	// 4. The .lb-controls pill SHOULD have its background (the
	// pill's bg was removed in Phase 88; restored in Phase 91
	// revert).
	if !strings.Contains(html, ".lb-controls {") {
		t.Fatal("no .lb-controls rule")
	}
	start := strings.Index(html, ".lb-controls {")
	end := strings.Index(html[start:], "}")
	rule := html[start : start+end+1]
	if !strings.Contains(rule, "background: rgba(255, 255, 255, 0.92)") {
		t.Errorf("expected .lb-controls to have its background (Phase 91: pill restored); rule: %q", rule)
	}
	if !strings.Contains(rule, "border-radius: 10px") {
		t.Errorf("expected .lb-controls to have border-radius: 10px (Phase 91: pill restored); rule: %q", rule)
	}
}

// TestRenderPage_Phase89ArrowPaddingLeft verifies Phase 89:
// the sort-by arrow (↑/↓) has padding-left so it sits
// further from the button label ("Name", "Type", etc.).
// Without this, the arrow touches the label text.
func TestRenderPage_Phase89ArrowPaddingLeft(t *testing.T) {
	files := []FileInfo{
		{Name: "img1.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The .sort-btn .arrow CSS rule should have padding-left
	// (so the arrow has breathing room from the label).
	if !strings.Contains(html, ".sort-btn .arrow {") {
		t.Fatal("no .sort-btn .arrow rule in CSS")
	}
	start := strings.Index(html, ".sort-btn .arrow {")
	end := strings.Index(html[start:], "}")
	rule := html[start : start+end+1]
	if !strings.Contains(rule, "padding-left") {
		t.Errorf("expected .sort-btn .arrow to have padding-left (Phase 89); rule: %q", rule)
	}
}

// TestRenderPage_Phase90ToggleNoAlignItems verifies Phase 90:
// the .section-toggle CSS rule no longer has align-items: center.
// (Without align-items, the character (− or +) uses its natural
// baseline instead of being vertically centered, which usually
// looks better for single-character buttons in a small square.)
func TestRenderPage_Phase90ToggleNoAlignItems(t *testing.T) {
	files := []FileInfo{
		{Name: "nested1", Kind: KindDir, ModTime: 100},
	}
	html, err := RenderPage("subdir", "./", "./_thumbs/", "subdir", "", false, false, 0, files, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(html, ".section-toggle {") {
		t.Fatal("no .section-toggle rule")
	}
	start := strings.Index(html, ".section-toggle {")
	end := strings.Index(html[start:], "}")
	rule := html[start : start+end+1]
	if strings.Contains(rule, "align-items:") {
		t.Errorf("expected .section-toggle to NOT have align-items (Phase 90: removed); rule: %q", rule)
	}
}

// TestParseTypeFilter_EmptyAndNil verifies that:
//   - no ?type= param => nil map
//   - ?type= (empty) => nil map
//   - ?type=   (whitespace) => nil map
func TestParseTypeFilter_EmptyAndNil(t *testing.T) {
	cases := []struct {
		name  string
		query url.Values
	}{
		{"no type param", url.Values{}},
		{"empty type param", url.Values{"type": {""}}},
		{"whitespace only", url.Values{"type": {"   "}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseTypeFilter(c.query)
			if got != nil {
				t.Errorf("expected nil for %q, got %v", c.query.Get("type"), got)
			}
		})
	}
}

// TestParseTypeFilter_Normalisation verifies the parser:
//   - lowercase: "JPG" -> ".jpg"
//   - dot prefix added: "jpg" -> ".jpg", ".jpg" -> ".jpg"
//   - whitespace trimmed: " jpg " -> ".jpg"
//   - empty entries skipped: ",,jpg,," -> {".jpg"}
//   - single entry: "jpg" -> {".jpg"}
//   - multiple entries: "jpg,png" -> {".jpg":true, ".png":true}
func TestParseTypeFilter_Normalisation(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  map[string]bool
	}{
		{"single, no dot, lowercase", "jpg", map[string]bool{".jpg": true}},
		{"single, with dot", ".jpg", map[string]bool{".jpg": true}},
		{"single, uppercase", "JPG", map[string]bool{".jpg": true}},
		{"single, mixed case, with dot", ".HeIc", map[string]bool{".heic": true}},
		{"single, whitespace", "  jpg  ", map[string]bool{".jpg": true}},
		{"multiple", "jpg,png", map[string]bool{".jpg": true, ".png": true}},
		{"multiple, mixed", "JPG, .png, MP4", map[string]bool{".jpg": true, ".png": true, ".mp4": true}},
		{"empty entries skipped", ",,jpg,,", map[string]bool{".jpg": true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseTypeFilter(url.Values{"type": {c.input}})
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("input %q: got %v, want %v", c.input, got, c.want)
			}
		})
	}
}

// TestTypeFilterActive verifies the boolean predicate: true if
// the URL has a non-empty ?type= value, false otherwise.
func TestTypeFilterActive(t *testing.T) {
	cases := []struct {
		name  string
		query url.Values
		want  bool
	}{
		{"no param", url.Values{}, false},
		{"empty", url.Values{"type": {""}}, false},
		{"whitespace", url.Values{"type": {"  "}}, false},
		{"value", url.Values{"type": {"jpg"}}, true},
		{"value with whitespace", url.Values{"type": {"  jpg  "}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := typeFilterActive(c.query); got != c.want {
				t.Errorf("query=%v: got %v, want %v", c.query, got, c.want)
			}
		})
	}
}

// TestApplyTypeFilter verifies the filter's effect on a file list:
//   - nil filter = pass-through
//   - empty filter (non-nil, no entries) = return empty
//   - non-empty filter = keep only matching
//   - case-insensitive (files may have .JPG ext; filter .jpg)
func TestApplyTypeFilter(t *testing.T) {
	files := []FileInfo{
		{Name: "a.jpg", Kind: KindImage},
		{Name: "b.JPG", Kind: KindImage},
		{Name: "c.png", Kind: KindImage},
		{Name: "d.mp4", Kind: KindVideo},
		{Name: "e.txt", Kind: KindOther},
		{Name: "f.tar.gz", Kind: KindOther},
	}

	t.Run("nil filter = pass-through", func(t *testing.T) {
		got := applyTypeFilter(files, nil)
		if len(got) != len(files) {
			t.Errorf("expected %d files, got %d", len(files), len(got))
		}
	})

	t.Run("empty filter = no files", func(t *testing.T) {
		got := applyTypeFilter(files, map[string]bool{})
		if len(got) != 0 {
			t.Errorf("expected 0 files, got %d", len(got))
		}
	})

	t.Run("filter to .jpg only (case-insensitive)", func(t *testing.T) {
		got := applyTypeFilter(files, map[string]bool{".jpg": true})
		if len(got) != 2 {
			t.Errorf("expected 2 files (a.jpg, b.JPG), got %d", len(got))
		}
		for _, f := range got {
			if f.Name != "a.jpg" && f.Name != "b.JPG" {
				t.Errorf("unexpected file: %q", f.Name)
			}
		}
	})

	t.Run("filter to .jpg + .png", func(t *testing.T) {
		got := applyTypeFilter(files, map[string]bool{".jpg": true, ".png": true})
		if len(got) != 3 {
			t.Errorf("expected 3 files, got %d", len(got))
		}
	})

	t.Run("filter to .gz (multi-dot files)", func(t *testing.T) {
		// filepath.Ext returns ".gz" for "f.tar.gz"
		got := applyTypeFilter(files, map[string]bool{".gz": true})
		if len(got) != 1 || got[0].Name != "f.tar.gz" {
			t.Errorf("expected f.tar.gz, got %+v", got)
		}
	})
}

// TestRenderPage_TypeFilter verifies the server-side filter
// works end-to-end: ?type=jpg in the URL shows only the jpg
// files, and the rendered HTML reflects the filter state.
func TestRenderPage_TypeFilter(t *testing.T) {
	files := []FileInfo{
		{Name: "alpha.jpg", ModTime: 100, Size: 1000, Kind: KindImage},
		{Name: "beta.png", ModTime: 90, Size: 2000, Kind: KindImage},
		{Name: "gamma.mp4", ModTime: 80, Size: 3000, Kind: KindVideo},
		{Name: "notes.txt", ModTime: 70, Size: 100, Kind: KindOther},
	}

	// No filter — all files should appear
	all, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, url.Values{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all, "alpha.jpg") || !strings.Contains(all, "beta.png") {
		t.Errorf("no-filter render should include all files")
	}
	if !strings.Contains(all, "gamma.mp4") {
		t.Errorf("no-filter render should include gamma.mp4")
	}
	if !strings.Contains(all, "notes.txt") {
		t.Errorf("no-filter render should include notes.txt")
	}

	// Filter to images only
	img, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, url.Values{
		"type": {"jpg,png"},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(img, "alpha.jpg") {
		t.Errorf("filtered render should include alpha.jpg")
	}
	if !strings.Contains(img, "beta.png") {
		t.Errorf("filtered render should include beta.png")
	}
	if strings.Contains(img, "gamma.mp4") {
		t.Errorf("filtered render should NOT include gamma.mp4 (not in filter)")
	}
	if strings.Contains(img, "notes.txt") {
		t.Errorf("filtered render should NOT include notes.txt (other-files are also filtered out)")
	}
}

// TestComputeBreadcrumb verifies the breadcrumb segment
// construction for various URL depths.
func TestComputeBreadcrumb(t *testing.T) {
	cases := []struct {
		name       string
		relPath    string
		title      string
		pathPrefix string
		want       []BreadcrumbSegment
	}{
		// relPath == "" uses the title as the root (no other segments).
		{
			name:       "gallery root (no subdir)",
			relPath:    "",
			title:      "images",
			pathPrefix: "./",
			want:       []BreadcrumbSegment{{Name: "images", Href: "./"}},
		},
		// relPath == "/" (just the leading slash) also uses the title.
		{
			name:       "root with trailing slash only",
			relPath:    "/",
			title:      "images",
			pathPrefix: "./",
			want:       []BreadcrumbSegment{{Name: "images", Href: "./"}},
		},
		// When relPath is non-empty, the FIRST segment of relPath
		// is the root (NOT the title). The title is unused.
		{
			name:       "one level deep",
			relPath:    "images/photos/",
			title:      "title-not-used",
			pathPrefix: "./",
			want: []BreadcrumbSegment{
				{Name: "images", Href: "./"},
				{Name: "photos", Href: "./photos/"},
			},
		},
		{
			name:       "three levels deep",
			relPath:    "images/photos/2024/maui/",
			title:      "title-not-used",
			pathPrefix: "./",
			want: []BreadcrumbSegment{
				{Name: "images", Href: "./"},
				{Name: "photos", Href: "./photos/"},
				{Name: "2024", Href: "./photos/2024/"},
				{Name: "maui", Href: "./photos/2024/maui/"},
			},
		},
		// Real-world case the user reported: relPath "images/photos/"
		// → breadcrumb should be "images / photos" (NOT "photos / images
		// / photos" which the old logic produced because it used the
		// title as the root).
		{
			name:       "user-reported case (URL /images/photos/)",
			relPath:    "images/photos/",
			title:      "photos",
			pathPrefix: "./",
			want: []BreadcrumbSegment{
				{Name: "images", Href: "./"},
				{Name: "photos", Href: "./photos/"},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := computeBreadcrumb(c.relPath, c.title, c.pathPrefix)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestRenderPage_Breadcrumb verifies the breadcrumb HTML is
// rendered in the output for a subdirectory. The last segment
// should be the current dir (rendered as plain text, not a
// link); intermediate segments should be links to the
// corresponding levels.
func TestRenderPage_Breadcrumb(t *testing.T) {
	files := []FileInfo{
		{Name: "alpha.jpg", ModTime: 100, Size: 100, Kind: KindImage},
	}
	html, err := RenderPage("images", "./", "./_thumbs/", "photos/2024/", "", false, false, 0, files, url.Values{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The breadcrumb nav should be present
	if !strings.Contains(html, `class="breadcrumb"`) {
		t.Error("expected breadcrumb nav in rendered HTML")
	}
	// Each segment should be present
	for _, name := range []string{"images", "photos", "2024"} {
		if !strings.Contains(html, name) {
			t.Errorf("expected breadcrumb segment %q in HTML", name)
		}
	}
	// The "current" segment class should be on the last item
	if !strings.Contains(html, `class="breadcrumb-current"`) {
		t.Error("expected the current segment to have class breadcrumb-current")
	}
	// The separator should be present
	if !strings.Contains(html, `class="breadcrumb-sep"`) {
		t.Error("expected breadcrumb separator in HTML")
	}
}

// TestRenderPage_Breadcrumb_PreservesFilter verifies that the
// breadcrumb links preserve the active ?type= filter. (When the
// user clicks a breadcrumb segment while a filter is active,
// the filter should be carried over to the new URL.)
func TestRenderPage_Breadcrumb_PreservesFilter(t *testing.T) {
	files := []FileInfo{{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage}}
	html, err := RenderPage("title-not-used", "./", "./_thumbs/", "images/photos/", "", false, false, 0, files, url.Values{
		"type": {"jpg,png"},
	}, defaultImageExts, defaultVideoExts)
	if err != nil {
		t.Fatal(err)
	}
	// The breadcrumb link for "images" (the root) should have
	// ?type=jpg,png appended.
	// The comma in the filter value is URL-encoded by html/template
	// (so the URL is parseable). We expect the percent-encoded form.
	if !strings.Contains(html, `href="./?type=jpg%2cpng"`) {
		t.Errorf("expected the root breadcrumb link to preserve the ?type= filter (URL-encoded); HTML excerpt: %s",
			substringAround(html, "breadcrumb", 200))
	}
	// The "photos" segment is the CURRENT dir (per relPath
	// "photos/"), so it should be plain text, not a link.
}

// substringAround returns 200 chars of s centered on the
// first occurrence of needle, or s if not found.

// TestComputeFilterGroups verifies the filter data
// construction:
//   - extensions are categorised by Image / Video / Other
//   - counts are correct
//   - Selected flag is set correctly based on the active filter
//   - DisplayExt preserves the canonical case
//   - Options are sorted alphabetically
//   - Empty file list returns three empty groups
func TestComputeFilterGroups(t *testing.T) {
	files := []FileInfo{
		{Name: "photo.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "photo2.JPG", ModTime: 2, Size: 200, Kind: KindImage}, // uppercase
		{Name: "anim.png", ModTime: 3, Size: 300, Kind: KindImage},
		{Name: "movie.mp4", ModTime: 4, Size: 400, Kind: KindVideo},
		{Name: "movie2.WEBM", ModTime: 5, Size: 500, Kind: KindVideo}, // uppercase
		{Name: "doc.pdf", ModTime: 6, Size: 100, Kind: KindOther},
		{Name: "archive.tar.gz", ModTime: 7, Size: 700, Kind: ImageTypeOther()}, // .gz
	}
	// Build the default image/video ext maps for the test
	imgExts := defaultImageExts
	vidExts := defaultVideoExts

	activeFilter := map[string]bool{".jpg": true, ".mp4": true}

	img, vid, other := computeFilterGroups(files, imgExts, vidExts, activeFilter)

	// Image group: .jpg (count 2, selected, displayExt=photo.jpg since
	// "photo.jpg" was first), .png (count 1, not selected)
	if img.Label != "Images" {
		t.Errorf("img label: got %q, want Images", img.Label)
	}
	if img.Total != 2 {
		t.Errorf("img total: got %d, want 2", img.Total)
	}
	if img.Selected != 1 {
		t.Errorf("img selected: got %d, want 1", img.Selected)
	}
	// Find the .jpg and .png options
	var jpgOpt, pngOpt *FilterOption
	for i, o := range img.Options {
		if o.Ext == ".jpg" {
			jpgOpt = &img.Options[i]
		}
		if o.Ext == ".png" {
			pngOpt = &img.Options[i]
		}
	}
	if jpgOpt == nil || pngOpt == nil {
		t.Fatalf("missing image options: %+v", img.Options)
	}
	if jpgOpt.Count != 2 {
		t.Errorf(".jpg count: got %d, want 2", jpgOpt.Count)
	}
	if !jpgOpt.Selected {
		t.Error(".jpg should be Selected (in active filter)")
	}
	if pngOpt.Selected {
		t.Error(".png should NOT be Selected (not in active filter)")
	}
	// DisplayExt should preserve the canonical case. The first
	// file we see with this ext wins, so .jpg shows "JPG" or "jpg"
	// depending on iteration order (NOT a guarantee, just check
	// it's non-empty and starts with a dot).
	if jpgOpt.DisplayExt == "" {
		t.Error("jpgOpt.DisplayExt is empty")
	}
	if jpgOpt.DisplayExt[0] != '.' {
		t.Errorf("jpgOpt.DisplayExt should start with '.', got %q", jpgOpt.DisplayExt)
	}
	// Options should be sorted alphabetically
	if len(img.Options) >= 2 && img.Options[0].DisplayExt > img.Options[1].DisplayExt {
		t.Errorf("img options not sorted: %v", img.Options)
	}

	// Video group: .mp4 (selected), .webm (not selected)
	if vid.Label != "Videos" {
		t.Errorf("vid label: got %q, want Videos", vid.Label)
	}
	if vid.Total != 2 || vid.Selected != 1 {
		t.Errorf("vid total/selected: got %d/%d, want 2/1", vid.Total, vid.Selected)
	}

	// Other group: .pdf, .gz
	if other.Label != "Other" {
		t.Errorf("other label: got %q, want Other", other.Label)
	}
	if other.Total != 2 {
		t.Errorf("other total: got %d, want 2", other.Total)
	}
	// Other shouldn't be Selected for anything
	if other.Selected != 0 {
		t.Errorf("other selected: got %d, want 0", other.Selected)
	}
}

// ImageTypeOther is a small helper that returns KindOther (used
// in TestComputeFilterGroups to put a .tar.gz file in the
// "other" group regardless of which class it actually is —
// tar.gz is in the "other" list by default).
func ImageTypeOther() FileKind { return KindOther }

// TestComputeFilterGroups_Empty verifies the helper handles
// the edge case of no files.
func TestComputeFilterGroups_Empty(t *testing.T) {
	img, vid, other := computeFilterGroups(nil, defaultImageExts, defaultVideoExts, nil)
	if img.Total != 0 || img.Selected != 0 || len(img.Options) != 0 {
		t.Errorf("empty img group: %+v", img)
	}
	if vid.Total != 0 || vid.Selected != 0 {
		t.Errorf("empty vid group: %+v", vid)
	}
	if other.Total != 0 {
		t.Errorf("empty other group: %+v", other)
	}
}

// TestComputeFilterGroups_NilFilter verifies that a nil active
// filter (no ?type= in the URL) means no options are marked
// as Selected.
func TestComputeFilterGroups_NilFilter(t *testing.T) {
	files := []FileInfo{{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage}}
	img, _, _ := computeFilterGroups(files, defaultImageExts, defaultVideoExts, nil)
	if img.Selected != 0 {
		t.Errorf("expected 0 selected with nil filter, got %d", img.Selected)
	}
	if img.Options[0].Selected {
		t.Error("expected .jpg NOT to be Selected with nil filter")
	}
}

// TestRenderPage_FilterUI verifies the server renders the
// filter UI correctly:
//   - <form class="filter-form"> with method="get" and action=""
//   - The "All" pill is present and has the active class when
//     no filter is active
//   - Each filter group (Images / Videos / Other) renders a
//     <details> element with checkboxes
//   - Each option has the right name, value, and checked state
//   - The Apply button is present
//   - When a filter is active, the All pill does NOT have
//     filter-pill-active
func TestRenderPage_FilterUI(t *testing.T) {
	files := []FileInfo{
		{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "b.png", ModTime: 2, Size: 200, Kind: KindImage},
		{Name: "c.mp4", ModTime: 3, Size: 300, Kind: KindVideo},
		{Name: "d.pdf", ModTime: 4, Size: 100, Kind: KindOther},
	}

	t.Run("no filter active", func(t *testing.T) {
		html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, defaultImageExts, defaultVideoExts)
		if err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(html, `class="filter-form"`) {
			t.Error("expected filter form in HTML")
		}
		if !strings.Contains(html, `filter-all filter-pill-active`) {
			t.Error("expected the 'All' pill to be active when no filter is active")
		}
		if !strings.Contains(html, `class="filter-apply"`) {
			t.Error("expected an Apply button")
		}
		// Each option should be present
		for _, ext := range []string{".jpg", ".png", ".mp4", ".pdf"} {
			if !strings.Contains(html, `value="`+ext+`"`) {
				t.Errorf("expected option for %q", ext)
			}
		}
		// The (0/N) count should appear for each group
		if !strings.Contains(html, `(0/2)`) {
			t.Error("expected image count (0/2) — 0 selected out of 2")
		}
		if !strings.Contains(html, `(0/1)`) {
			t.Error("expected video count (0/1) and other count (0/1)")
		}
		// No checkbox should be checked
		if strings.Contains(html, `checked`) {
			t.Error("no checkboxes should be checked when no filter is active")
		}
	})

	t.Run("with ?type=jpg filter", func(t *testing.T) {
		html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, url.Values{
			"type": {"jpg"},
		}, defaultImageExts, defaultVideoExts)
		if err != nil {
			t.Fatal(err)
		}

		// The All pill should NOT be active
		if strings.Contains(html, `class="filter-all filter-pill-active"`) {
			t.Error("'All' should NOT be active when a filter is active")
		}
		// The Images dropdown should be open (has selected options)
		// — we check via the open attribute or via the [open]
		// in the dropdown container
		if !strings.Contains(html, `class="filter-dropdown" open`) &&
			!strings.Contains(html, `class="filter-dropdown" open=""`) {
			// html/template renders the open attr as ` open` (no value)
			// when set to true
			if !strings.Contains(html, `<details class="filter-dropdown" open`) {
				t.Error("expected the Images dropdown to be open (has selected options)")
			}
		}
		// The .jpg option should be checked
		if !strings.Contains(html, `value=".jpg" checked`) {
			t.Error("expected .jpg checkbox to be checked")
		}
		// The (1/2) count should appear (1 selected out of 2)
		if !strings.Contains(html, `(1/2)`) {
			t.Error("expected image count (1/2) — 1 selected out of 2")
		}
	})
}

// TestRenderPage_MediaSectionHasToggle verifies that the
// Media section has the same show/hide toggle pattern as
// Directories + Other files (data-section attribute + toggle
// button + section-body wrapper + heading-divider line).
func TestRenderPage_MediaSectionHasToggle(t *testing.T) {
	files := []FileInfo{
		{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "b.png", ModTime: 2, Size: 200, Kind: KindImage},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil, defaultImageExts, defaultVideoExts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "data-section=") || !strings.Contains(html, "media") {
		t.Error("expected media section to have data-section attribute")
	}
	if !strings.Contains(html, "data-toggle=") || !strings.Contains(html, "media") {
		t.Error("expected media section to have a toggle button with data-toggle attribute")
	}
	if !strings.Contains(html, "aria-controls=") {
		t.Error("expected toggle button to have aria-controls attribute")
	}
	if !strings.Contains(html, "media-body") {
		t.Error("expected section-body wrapper with id=media-body")
	}
	if !strings.Contains(html, "Media (2)") {
		t.Error("expected heading to show Media (N) where N is the file count")
	}
}
func substringAround(s, needle string, width int) string {
	idx := strings.Index(s, needle)
	if idx < 0 {
		return s
	}
	start := idx - width/2
	if start < 0 {
		start = 0
	}
	end := idx + width/2
	if end > len(s) {
		end = len(s)
	}
	return s[start:end]
}

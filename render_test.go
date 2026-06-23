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
	tmp, err := os.MkdirTemp("", "caddy-image-gallery-test-*")
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
	html, err := RenderPage("Test Gallery", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
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
	html, err := RenderPage("x", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
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
	html, err := RenderPage("t", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	// First page: should have "Next" but not "← Prev" as a link
	if !strings.Contains(html, `href="?sort=mtime&order=desc&page=2"`) {
		t.Error("expected Next link to page 2")
	}
	// Test page 2
	q := url.Values{"page": {"2"}}
	html2, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, q)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 10, files, nil)
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
	html25, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 25, files, nil)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 10, files, nil)
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
	html2, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 10, files2, nil)
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
	html, _ := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
	if !strings.Contains(html, `href="?sort=name&order=asc"`) {
		t.Error("expected default Name link to be asc (clicking activates sort)")
	}

	// Now activate by name asc. The link should toggle to desc.
	q := url.Values{"sort": {"name"}, "order": {"asc"}}
	html, _ = RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, q)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
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
	html, err := RenderPage("empty", "./", "./_thumbs/", "", "", false, false, 0, nil, nil)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The image section header should appear exactly once.
	if c := strings.Count(html, ">Media<"); c != 1 {
		t.Errorf("expected exactly one 'Images' section, got %d", c)
	}
	// The "Other files" section should appear exactly once.
	if c := strings.Count(html, "Other files"); c != 1 {
		t.Errorf("expected exactly one 'Other files' section, got %d", c)
	}
	// notes.txt should be in the "Other files" section.
	// clip.mp4 should be in the image grid section (with a play-button).
	othersIdx := strings.Index(html, "Other files")
	imagesIdx := strings.Index(html, ">Media<")
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
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
	imgStart := strings.Index(html, ">Media<")
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
	html, err := RenderPage("subdir", "./", "./_thumbs/", "subdir", "", false, false, 0, files, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 1. The Up entry must be the FIRST ROW of the dirs table
	//    (Phase 72: moved from a separate <div class="up-chip-row">
	//    above the table to a <tr class="up-row"> inside the
	//    table's <tbody>).
	upRowStart := strings.Index(html, `<tr class="up-row">`)
	if upRowStart < 0 {
		t.Fatal(`expected a <tr class="up-row"> containing the Up entry`)
	}
	upRowEnd := strings.Index(html[upRowStart:], `</tr>`)
	if upRowEnd < 0 {
		t.Fatal(`could not find end of up-row tr`)
	}
	upRow := html[upRowStart : upRowStart+upRowEnd]
	// The Up row should have a single <td colspan="3"> spanning
	// all 3 columns of the dirs table.
	if !strings.Contains(upRow, `colspan="3"`) {
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
	if upRowStart < dirsTableStart {
		t.Error("expected up-row to be INSIDE the dirs-table (after dirsTableStart)")
	}
	dirsTableEnd := strings.Index(html[dirsTableStart:], `</table>`)
	if dirsTableEnd < 0 {
		t.Fatal(`could not find end of dirs-table`)
	}
	dirsTable := html[dirsTableStart : dirsTableStart+dirsTableEnd]
	// Per Phase 72: the Up entry is now a row INSIDE the dirs
	// table (not a separate chip above it). The dirs-table
	// starts with the up-row, then the up-spacer (blank) row,
	// then the actual subdirs. We check that the up entry's
	// href="../" is in the table (in the up-row), AND that the
	// subdirs are also in the table.
	if !strings.Contains(dirsTable, `href="../"`) {
		t.Error(`expected up entry's href="../" to be in the dirs-table (in the up-row)`)
	}
	if !strings.Contains(dirsTable, "nested1/") {
		t.Error("expected nested1 subdir in dirs-table")
	}
	if !strings.Contains(dirsTable, "nested2/") {
		t.Error("expected nested2 subdir in dirs-table")
	}
	// The up-spacer row should be present (Phase 72: a blank
	// row between the Up entry and the directory list).
	if !strings.Contains(dirsTable, `class="up-spacer"`) {
		t.Error("expected a <tr class=\"up-spacer\"> in the dirs-table (blank row between Up and subdirs)")
	}
	// Each subdir should be in its own <tr> with the directory
	// name in a Name cell (a .col-name <td>) — verify the
	// table has at least 4 rows in the tbody (up-row + spacer +
	// 2 subdirs). Count any <tr...> (not just bare <tr>) since
	// the up-spacer row has a class attribute.
	rowCount := strings.Count(dirsTable, "<tr")
	if rowCount < 4 {
		t.Errorf("expected at least 4 <tr...> rows (up-row + spacer + 2 subdirs), got %d", rowCount)
	}

	// 3. The dirs-row should NOT contain the images (the image
	//    grid is a separate section, comes after the dirs
	//    section in the page).
	othersIdx := strings.Index(html, "Other files")
	if othersIdx < 0 {
		othersIdx = len(html)
	}
	dirsSection := html[:othersIdx]
	// Per Phase 72: the up-row is now inside the dirs-table, so
	// the dirs section should contain the dirs-table (not the
	// old up-chip-row div).
	if !strings.Contains(dirsSection, `class="files-table dirs-table"`) {
		t.Error(`expected dirs section to contain the dirs-table`)
	}
	if !strings.Contains(dirsSection, `class="up-row"`) {
		t.Error(`expected dirs section to contain the up-row (now inside the dirs-table)`)
	}
	// Old up-chip-row should be GONE.
	if strings.Contains(dirsSection, `class="up-chip-row"`) {
		t.Error(`expected NO up-chip-row in dirs section (replaced by up-row in Phase 72)`)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
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
	html, err := RenderPage("root", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
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
			html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, q)
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
		html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
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
		html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, true, 0, files, nil)
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
		html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, true, 0, files, nil)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
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
	imagesIdx := strings.Index(html, ">Media<")
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
			html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 8, files25, q)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 50, files4, q)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 50, files, q)
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
			html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
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
	html2, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, filesNoVideo, nil)
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
	html3, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, filesAllVideo, nil)
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
			html, err := RenderPage("test", "./", "./_thumbs/", tc.relPath, "", false, false, 0, files, nil)
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
			upRowStart := strings.Index(html, `<tr class="up-row">`)
			if upRowStart < 0 {
				t.Fatalf("expected an up-row for relPath %q", tc.relPath)
			}
			upRowEnd := strings.Index(html[upRowStart:], `</tr>`)
			upRow := html[upRowStart : upRowStart+upRowEnd]
			if !strings.Contains(upRow, tc.wantText) {
				t.Errorf("up-row for relPath %q: expected text %q, got: %q", tc.relPath, tc.wantText, upRow)
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
		imgStart := strings.Index(html[othersStart:], ">Media<")
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
			url.Values{"sort": []string{"name"}, "order": []string{"asc"}})
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
			url.Values{"sort": []string{"name"}, "order": []string{"desc"}})
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
			url.Values{"sort": []string{"mtime"}, "order": []string{"asc"}})
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
			url.Values{"sort": []string{"size"}, "order": []string{"asc"}})
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
				url.Values{"sort": []string{s.field}, "order": []string{s.order}})
			if err != nil {
				t.Fatal(err)
			}
			// Debug: print the relevant portion of HTML
			// (Phase 71: the heading now wraps the title in a <span>
			// for the flex layout, so we search for "Directories"
			// and "Other" anywhere in the heading rather than the
			// old direct match.)
			dirsIdx := strings.Index(html, ">Directories<")
			othersIdx := strings.Index(html, ">Other files<")
			mediaIdx := strings.Index(html, ">Media<")
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
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
//  6. The sort-btn.active .arrow is white in both modes
func TestRenderPage_Phase72UIChanges(t *testing.T) {
	// Set up a gallery with a dir + an image, in a subdir
	// (so we have an up-row to render).
	files := []FileInfo{
		{Name: "nested1", Kind: KindDir, ModTime: 100},
		{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	html, err := RenderPage("subdir", "./", "./_thumbs/", "subdir", "", false, false, 0, files, nil)
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

	// 3. The up-row is the FIRST <tr> in the dirs-table's <tbody>.
	// (The thead also has a <tr> for the column headers, so we
	// skip past the thead to find the tbody's first <tr>.)
	dirsTableStart := strings.Index(html, `<table class="files-table dirs-table">`)
	if dirsTableStart < 0 {
		t.Fatal("no dirs-table found")
	}
	upRowStart := strings.Index(html, `<tr class="up-row">`)
	if upRowStart < 0 {
		t.Fatal("no up-row found in dirs-table")
	}
	if upRowStart < dirsTableStart {
		t.Error("up-row should be INSIDE dirs-table (after dirsTableStart)")
	}
	// The up-row should be the FIRST <tr> AFTER the <tbody> opening
	// tag (not counting the thead <tr>).
	dirsTableEnd := strings.Index(html[dirsTableStart:], `</table>`)
	dirsTable := html[dirsTableStart : dirsTableStart+dirsTableEnd]
	tbodyStart := strings.Index(html[dirsTableStart:], `<tbody>`)
	if tbodyStart < 0 {
		t.Fatal("no <tbody> in dirs-table")
	}
	tbodyStart += dirsTableStart
	// Find the first <tr...> AFTER <tbody>
	trAfterTbody := strings.Index(html[tbodyStart:], `<tr`)
	if trAfterTbody < 0 {
		t.Fatal("no <tr> in tbody")
	}
	trAfterTbody += tbodyStart
	// Check if the up-row is at or near this position
	upRowInTbody := strings.Index(html[tbodyStart:], `<tr class="up-row">`)
	if upRowInTbody < 0 {
		t.Error("no up-row in tbody")
	}
	upRowInTbody += tbodyStart
	// The first <tr> after <tbody> should be the up-row.
	if trAfterTbody != upRowInTbody {
		t.Errorf("expected up-row to be the first <tr> in tbody (got offset %d, first <tr at %d)", upRowInTbody, trAfterTbody)
	}

	// 4. The dirs table has an up-spacer row after the up-row.
	if !strings.Contains(dirsTable, `<tr class="up-spacer"`) {
		t.Error(`expected <tr class="up-spacer"> row in dirs-table (Phase 72)`)
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

	// 6. The sort-btn.active .arrow is white in both modes.
	if !strings.Contains(html, `.sort-btn.active .arrow { color: white; }`) {
		t.Error(`expected .sort-btn.active .arrow { color: white; } (Phase 72: white in both modes)`)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
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

	// The dirs row (for "alpha") should have cell-link anchors in
	// the Type and Date cells (not just the Name cell). We start
	// the row boundary at the start of the <tr> (searching back
	// from "alpha/" since the Name link's href="./alpha/" is
	// positioned BEFORE the "alpha/" text content in the source).
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
	if cellLinks != 2 {
		t.Errorf("expected 2 cell-links in the alpha row (Type + Date columns), got %d in row: %q", cellLinks, alphaRow)
	}
	// All cell-links should have the same href (./alpha/).
	hrefCount := strings.Count(alphaRow, `href="./alpha/"`)
	if hrefCount != 3 {
		t.Errorf("expected 3 anchors with href=./alpha/ (Name + 2 cell-links), got %d", hrefCount)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0, files, nil)
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

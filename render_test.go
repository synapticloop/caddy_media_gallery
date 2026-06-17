package gallery

import (
	"net/url"
	"os"
	"path/filepath"
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
	html, err := RenderPage("Test Gallery", "./", "./_thumbs/", "", "", false, 0, files, nil)
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
	html, err := RenderPage("x", "./", "./_thumbs/", "", "", false, 0, files, nil)
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
	html, err := RenderPage("t", "./", "./_thumbs/", "", "", false, 0, files, nil)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, 0, files, nil)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, 0, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	// First page: should have "Next" but not "← Prev" as a link
	if !strings.Contains(html, `href="?sort=mtime&order=desc&page=2"`) {
		t.Error("expected Next link to page 2")
	}
	// Test page 2
	q := url.Values{"page": {"2"}}
	html2, err := RenderPage("test", "./", "./_thumbs/", "", "", false, 0, files, q)
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
	html, _ := RenderPage("test", "./", "./_thumbs/", "", "", false, 0, files, nil)
	if !strings.Contains(html, `href="?sort=name&order=asc"`) {
		t.Error("expected default Name link to be asc (clicking activates sort)")
	}

	// Now activate by name asc. The link should toggle to desc.
	q := url.Values{"sort": {"name"}, "order": {"asc"}}
	html, _ = RenderPage("test", "./", "./_thumbs/", "", "", false, 0, files, q)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, 0, files, nil)
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
	html, err := RenderPage("empty", "./", "./_thumbs/", "", "", false, 0, nil, nil)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, 0, files, nil)
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
	html, err := RenderPage("subdir", "./", "./_thumbs/", "subdir", "", false, 0, files, nil)
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
	html, err := RenderPage("root", "./", "./_thumbs/", "", "", false, 0, files, nil)
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
			html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, 0, files, q)
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
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, 0, files, nil)
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
	imagesIdx := strings.Index(html, ">Images<")
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

func TestRenderPage_SortIndicatorInHeader(t *testing.T) {
	files := []FileInfo{
		{Name: "a.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	// Default sort: should show "Sort: Modified ↓" as a span (not a link).
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, 0, files, nil)
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
	html, _ = RenderPage("test", "./", "./_thumbs/", "", "", false, 0, files, q)
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

package gallery

import (
	"net/url"
	"strings"
	"testing"
)

// TestParseSearchQuery covers the basic split + lowercase
// behaviour. Per Phase 118 design: trim, lowercase, split on
// whitespace, empty result means "no filter".
func TestParseSearchQuery(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"whitespace only", "   ", nil},
		{"single word", "cat", []string{"cat"}},
		{"multiple words", "cat photo", []string{"cat", "photo"}},
		{"mixed case", "Cat PHOTO", []string{"cat", "photo"}},
		{"extra whitespace", "  cat   photo  ", []string{"cat", "photo"}},
		{"tabs", "cat\tphoto", []string{"cat", "photo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSearchQuery(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestFilenameMatchesQuery verifies the word-boundary rule:
// split filename on _, -, and space; match if any filename
// word STARTS WITH any query word. Case-insensitive.
func TestFilenameMatchesQuery(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		query    []string
		want     bool
	}{
		// Per Phase 118 design examples.
		{"cat matches cat.jpg", "cat.jpg", []string{"cat"}, true},
		{"cat matches cat-photo.jpg", "cat-photo.jpg", []string{"cat"}, true},
		{"cat matches my_cat.webp", "my_cat.webp", []string{"cat"}, true},
		{"cat matches category-icon.svg", "category-icon.svg", []string{"cat"}, true},
		{"cat does NOT match scatter.png", "scatter.png", []string{"cat"}, false},
		{"cat photo matches cat-photo.jpg", "cat-photo.jpg", []string{"cat", "photo"}, true},
		{"case insensitive", "CAT.jpg", []string{"cat"}, true},
		{"no query = always match", "anything.jpg", nil, true},
		{"no query (empty slice) = always match", "anything.jpg", []string{}, true},
		{"underscore-separated filename", "my_cat_photo.jpg", []string{"photo"}, true},
		{"hyphen-separated filename", "my-cat-photo.jpg", []string{"photo"}, true},
		{"partial word does NOT match", "scatter.png", []string{"cat"}, false},
		// q word must be a PREFIX of a filename word (not
		// appear anywhere within a word).
		{"catt does not match cat", "cat.jpg", []string{"catt"}, false},
		{"photo matches photo.jpg but also photo-gallery.png", "photo-gallery.png", []string{"photo"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filenameMatchesQuery(tc.filename, tc.query, "word")
			if got != tc.want {
				t.Errorf(`filenameMatchesQuery(%q, %v, word) = %v, want %v`,
					tc.filename, tc.query, got, tc.want)
			}
		})
	}
}

// TestApplySearchFilter verifies the server-side filter
// behaviour: directories pass through, files are filtered by
// the word-boundary match. Empty query returns all files.
func TestApplySearchFilter(t *testing.T) {
	files := []FileInfo{
		{Name: "cat.jpg", Kind: KindImage},
		{Name: "cat-photo.jpg", Kind: KindImage},
		{Name: "scatter.png", Kind: KindImage},
		{Name: "my_cat.webp", Kind: KindImage},
		{Name: "subdir", Kind: KindDir},
		{Name: "category-icon.svg", Kind: KindImage},
		{Name: "notes.txt", Kind: KindOther},
	}
	// Empty query = all files returned.
	all := applySearchFilter(files, nil, "word")
	if len(all) != len(files) {
		t.Errorf("empty query: expected %d files, got %d", len(files), len(all))
	}
	// q=cat: all matching files (scatter excluded), subdir
	// passes through (dirs are NOT searched).
	matched := applySearchFilter(files, []string{"cat"}, "word")
	names := []string{}
	for _, f := range matched {
		names = append(names, f.Name)
	}
	want := []string{"cat.jpg", "cat-photo.jpg", "my_cat.webp", "subdir", "category-icon.svg"}
	if len(names) != len(want) {
		t.Errorf("q=cat: got %v, want %v", names, want)
	}
	for i := range names {
		if names[i] != want[i] {
			t.Errorf("q=cat: got %v, want %v", names, want)
			break
		}
	}
}

// TestRenderPage_SearchInputInFilterForm verifies that the
// rendered HTML includes the search input and "Search all"
// button inside the filter form.
func TestRenderPage_SearchInputInFilterForm(t *testing.T) {
	files := []FileInfo{
		{Name: "cat.jpg", ModTime: 1, Size: 100, Kind: KindImage},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0,
		[]string{"30", "60", "120", "all"}, files, nil, nil, nil, "", "", "substring")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `<input type="search" name="q"`) {
		t.Errorf("expected search input in the filter form, got: %q", html)
	}
	if !strings.Contains(html, `class="search-button"`) {
		t.Errorf("expected 'Search all' button in the filter form, got: %q", html)
	}
}

// TestRenderPage_SearchQueryFiltersFiles verifies that
// ?q=foo on the server side filters the rendered files.
func TestRenderPage_SearchQueryFiltersFiles(t *testing.T) {
	files := []FileInfo{
		{Name: "cat.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "dog.jpg", ModTime: 2, Size: 200, Kind: KindImage},
		{Name: "cat-photo.jpg", ModTime: 3, Size: 300, Kind: KindImage},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0,
		[]string{"30", "60", "120", "all"}, files,
		url.Values{"q": []string{"cat"}}, nil, nil, "", "", "substring")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "cat.jpg") {
		t.Error("expected cat.jpg in the rendered HTML for ?q=cat")
	}
	if !strings.Contains(html, "cat-photo.jpg") {
		t.Error("expected cat-photo.jpg in the rendered HTML for ?q=cat")
	}
	if strings.Contains(html, "dog.jpg") {
		t.Error("did NOT expect dog.jpg in the rendered HTML for ?q=cat")
	}
}

// TestRenderPage_EmptySearchShowsAllFiles verifies that an
// empty q parameter doesn't filter anything out.
func TestRenderPage_EmptySearchShowsAllFiles(t *testing.T) {
	files := []FileInfo{
		{Name: "cat.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "dog.jpg", ModTime: 2, Size: 200, Kind: KindImage},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0,
		[]string{"30", "60", "120", "all"}, files,
		url.Values{"q": []string{""}}, nil, nil, "", "", "substring")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "cat.jpg") {
		t.Error("expected cat.jpg for empty ?q=")
	}
	if !strings.Contains(html, "dog.jpg") {
		t.Error("expected dog.jpg for empty ?q=")
	}
}

// TestRenderPage_DataFilenameAttribute verifies that every
// .card and every .files-table tbody tr has a data-filename
// attribute (used by the JS client-side filter).
func TestRenderPage_DataFilenameAttribute(t *testing.T) {
	files := []FileInfo{
		{Name: "cat.jpg", ModTime: 1, Size: 100, Kind: KindImage},
		{Name: "notes.txt", ModTime: 2, Size: 200, Kind: KindOther},
	}
	html, err := RenderPage("test", "./", "./_thumbs/", "", "", false, false, 0,
		[]string{"30", "60", "120", "all"}, files, nil, nil, nil, "", "", "substring")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `data-filename="cat.jpg"`) {
		t.Error("expected data-filename on .card for cat.jpg")
	}
	if !strings.Contains(html, `data-filename="notes.txt"`) {
		t.Error("expected data-filename on <tr> for notes.txt")
	}
}

// TestFilenameMatchesQuery_Substring verifies the substring
// matching rule. Unlike the word-boundary rule (which
// requires the query to start a filename "word"),
// substring matches anywhere in the filename.
func TestFilenameMatchesQuery_Substring(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		query    []string
		want     bool
	}{
		// Per user request 2026-06-27: substring is the
		// DEFAULT mode (when the operator doesn't set
		// search_match). Most permissive: "cat" matches
		// any filename that contains "cat" anywhere.
		{"cat in cat.jpg", "cat.jpg", []string{"cat"}, true},
		{"cat in cat-photo.jpg", "cat-photo.jpg", []string{"cat"}, true},
		{"cat in my_cat.webp", "my_cat.webp", []string{"cat"}, true},
		{"cat in category-icon.svg", "category-icon.svg", []string{"cat"}, true},
		{"cat in scatter.png", "scatter.png", []string{"cat"}, true},
		{"cat in catnip.jpg", "catnip.jpg", []string{"cat"}, true},
		// Multi-word query: all words must appear (anywhere).
		{"cat + dog in catdog.jpg", "catdog.jpg", []string{"cat", "dog"}, true},
		{"cat + dog in dog-cat.jpg", "dog-cat.jpg", []string{"cat", "dog"}, true},
		{"cat + dog in just-cat.jpg (missing dog)", "just-cat.jpg", []string{"cat", "dog"}, false},
		// No match.
		{"no match", "photo.png", []string{"cat"}, false},
		// Empty query = no filter = always match.
		{"empty query", "any.jpg", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filenameMatchesQuery(tc.filename, tc.query, "substring")
			if got != tc.want {
				t.Errorf(`filenameMatchesQuery(%q, %v, substring) = %v, want %v`, tc.filename, tc.query, got, tc.want)
			}
		})
	}
}

// TestFilenameMatchesQuery_Word verifies the word-boundary
// matching rule. Query must match the start of a "word"
// (delimited by _, -, space). This is the opt-in mode
// (operator must set search_match word).
func TestFilenameMatchesQuery_Word(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		query    []string
		want     bool
	}{
		// Per user request 2026-06-27: "cat" matches
		// words starting with "cat" but NOT random positions.
		{"cat in cat.jpg", "cat.jpg", []string{"cat"}, true},
		{"cat in cat-photo.jpg", "cat-photo.jpg", []string{"cat"}, true},
		{"cat in my_cat.webp", "my_cat.webp", []string{"cat"}, true},
		{"cat in category-icon.svg", "category-icon.svg", []string{"cat"}, true},
		{"cat in catfish.jpg", "catfish.jpg", []string{"cat"}, true},
		// scatter: word "scatter" does NOT start with "cat"
		// (it starts with "s"). No match in word mode.
		{"cat in scatter.png (NOT a word match)", "scatter.png", []string{"cat"}, false},
		// catnip: word "catnip" starts with "cat" — match.
		{"cat in catnip.jpg (word starts with cat)", "catnip.jpg", []string{"cat"}, true},
		// dog in scatter.png: word "scatter" doesn't start with dog.
		{"dog in scatter.png (no match)", "scatter.png", []string{"dog"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filenameMatchesQuery(tc.filename, tc.query, "word")
			if got != tc.want {
				t.Errorf(`filenameMatchesQuery(%q, %v, word) = %v, want %v`, tc.filename, tc.query, got, tc.want)
			}
		})
	}
}

// TestFilenameMatchesQuery_DefaultMode verifies that an
// empty or invalid mode defaults to substring (the
// documented default). Per user request 2026-06-27.
func TestFilenameMatchesQuery_DefaultMode(t *testing.T) {
	// scatter.png contains "cat" — substring match returns
	// true; word match returns false. With the default
	// (empty string), we should get the substring behavior.
	got := filenameMatchesQuery("scatter.png", []string{"cat"}, "")
	if !got {
		t.Error(`expected default (empty mode) to match scatter.png with "cat" (substring behaviour)`)
	}
	// Invalid mode value also defaults to substring.
	got = filenameMatchesQuery("scatter.png", []string{"cat"}, "invalid-mode")
	if !got {
		t.Error(`expected invalid mode to default to substring`)
	}
}

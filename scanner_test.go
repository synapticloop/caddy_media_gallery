package gallery

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// helper: create N files in a fresh temp dir, return the dir.
func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		want FileKind
	}{
		{"photo.jpg", KindImage},
		{"photo.jpeg", KindImage},
		{"photo.JPG", KindImage}, // case-insensitive
		{"art.png", KindImage},
		{"anim.gif", KindImage},
		{"hero.webp", KindImage},
		{"logo.svg", KindImage},
		{"movie.mp4", KindVideo},
		{"clip.webm", KindVideo},
		{"notes.txt", KindOther},
		{"beach.html", KindOther},
		{"archive.tar.gz", KindOther},
		{"noext", KindOther},
	}
	for _, c := range cases {
		if got := Classify(c.name); got != c.want {
			t.Errorf("Classify(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestScanner_ClassifiesAndSorts(t *testing.T) {
	dir := writeFixture(t, map[string]string{
		"a.jpg":     "x",
		"b.png":     "x",
		"c.mp4":     "x",
		"notes.txt": "x",
	})
	// Create a subdir to verify the scanner includes directories
	// with Kind = KindDir.
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}
	s := NewScanner(dir)
	got, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	// Should be 5 entries (4 files + 1 subdir).
	if len(got) != 5 {
		t.Fatalf("expected 5 entries, got %d: %+v", len(got), got)
	}
	// Counts by kind.
	kinds := map[FileKind]int{}
	for _, f := range got {
		kinds[f.Kind]++
	}
	if kinds[KindImage] != 2 || kinds[KindVideo] != 1 || kinds[KindOther] != 1 || kinds[KindDir] != 1 {
		t.Errorf("kind counts: got %v, want image=2 video=1 other=1 dir=1", kinds)
	}
}

func TestScanner_DefaultSortIsMtimeDesc(t *testing.T) {
	dir := t.TempDir()
	// Write files in known order with a delay so mtimes are distinct.
	for _, name := range []string{"old.jpg", "mid.jpg", "new.jpg"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	s := NewScanner(dir) // default sort: mtime
	got, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"new.jpg", "mid.jpg", "old.jpg"}
	if len(got) != 3 {
		t.Fatalf("expected 3 files, got %d", len(got))
	}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("position %d: got %q, want %q (full: %v)", i, got[i].Name, w, names(got))
		}
	}
}

func TestScanner_SortByName(t *testing.T) {
	dir := writeFixture(t, map[string]string{
		"banana.jpg": "x",
		"Apple.jpg":  "x", // case-insensitive: A < b
		"cherry.jpg": "x",
	})
	s := &Scanner{Root: dir, Sort: "name"}
	got, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Apple.jpg", "banana.jpg", "cherry.jpg"}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("position %d: got %q, want %q (full: %v)", i, got[i].Name, w, names(got))
		}
	}
}

func TestScanner_BadRootReturnsError(t *testing.T) {
	s := NewScanner("/this/does/not/exist/anywhere")
	if _, err := s.Scan(); err == nil {
		t.Error("expected error for nonexistent root, got nil")
	}
}

func names(files []FileInfo) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Name
	}
	return out
}

package gallery

import (
	"bytes"
	"html/template"
	"os"
	"path/filepath"
	"strings"
)

// PageData is the data passed to the gallery template.
type PageData struct {
	Title       string
	PathPrefix  string
	ThumbPrefix string
	Images      []FileInfo
	Videos      []FileInfo
	OtherFiles  []FileInfo
}

const galleryTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Title}}</title>
<style>{{template "style.css" .}}</style>
</head>
<body>
<header>
  <h1>{{.Title}}</h1>
  <div class="meta">
    <span class="count">{{len .Images}} images{{if gt (len .Videos) 0}}, {{len .Videos}} videos{{end}}</span>
  </div>
</header>
<main>
  <div class="grid">
    {{range .Images}}
    <a class="card" href="{{$.PathPrefix}}{{.Name}}">
      <div class="thumb">
        <img loading="lazy" src="{{$.ThumbPrefix}}{{thumb .Name}}.webp" alt="{{.Name}}">
      </div>
      <div class="caption"><span>{{.Name}}</span></div>
    </a>
    {{end}}
  </div>
  {{if .Videos}}
  <h2 class="section-heading">Videos</h2>
  <div class="grid">
    {{range .Videos}}
    <a class="card video" href="{{$.PathPrefix}}{{.Name}}">
      <div class="thumb thumb-video">
        <video preload="none" muted loop playsinline>
          <source src="{{$.PathPrefix}}{{.Name}}" type="video/{{ext .Name}}">
        </video>
        <span class="play">▶</span>
      </div>
      <div class="caption"><span>{{.Name}}</span></div>
    </a>
    {{end}}
  </div>
  {{end}}
  {{if .OtherFiles}}
  <details class="other-files">
    <summary>Other files ({{len .OtherFiles}})</summary>
    <ul>
      {{range .OtherFiles}}
      <li><a href="{{$.PathPrefix}}{{.Name}}">{{.Name}}</a></li>
      {{end}}
    </ul>
  </details>
  {{end}}
</main>
</body>
</html>
`

// styleCSS is the dark-themed stylesheet, inlined in the template above.
// Cyberpunk/noir: near-black bg, cool blue accent, monospace headers.
const styleCSS = `
* { box-sizing: border-box; margin: 0; padding: 0; }
body {
  background: #0a0a0f;
  color: #d0d0d8;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
  min-height: 100vh;
  padding: 32px 24px 80px;
  line-height: 1.5;
}
header {
  max-width: 1200px;
  margin: 0 auto 28px;
  padding-bottom: 16px;
  border-bottom: 1px solid #1e1e2e;
  display: flex;
  justify-content: space-between;
  align-items: baseline;
}
h1 {
  font-family: 'Courier New', Consolas, monospace;
  font-size: 14px;
  font-weight: 700;
  color: #fff;
  letter-spacing: 0.14em;
  text-transform: uppercase;
}
.meta { font-size: 12px; color: #555; }
.count { letter-spacing: 0.05em; }
main { max-width: 1200px; margin: 0 auto; }
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 18px;
}
.card {
  display: block;
  background: #11111a;
  border: 1px solid #1a1a26;
  border-radius: 6px;
  overflow: hidden;
  text-decoration: none;
  color: inherit;
  transition: border-color 0.15s, transform 0.15s;
}
.card:hover {
  border-color: #5580ff;
  transform: translateY(-1px);
}
.thumb {
  position: relative;
  width: 100%;
  aspect-ratio: 1 / 1;
  background: #06060a;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
}
.thumb img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  display: block;
}
.thumb-video { background: #000; }
.thumb-video video {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.play {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  font-size: 2.5rem;
  color: rgba(255,255,255,0.85);
  text-shadow: 0 0 12px rgba(0,0,0,0.6);
  pointer-events: none;
}
.caption {
  padding: 8px 10px;
  font-size: 11px;
  color: #777;
  font-family: 'Courier New', Consolas, monospace;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.caption span { display: block; }
.section-heading {
  font-family: 'Courier New', Consolas, monospace;
  font-size: 12px;
  font-weight: 700;
  color: #888;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  margin: 36px 0 14px;
  padding-bottom: 8px;
  border-bottom: 1px solid #1e1e2e;
}
.other-files {
  margin-top: 40px;
  padding: 14px 18px;
  background: #0d0d14;
  border: 1px solid #1a1a26;
  border-radius: 6px;
}
.other-files summary {
  cursor: pointer;
  font-family: 'Courier New', Consolas, monospace;
  font-size: 12px;
  color: #888;
  letter-spacing: 0.08em;
  text-transform: uppercase;
}
.other-files ul {
  list-style: none;
  margin-top: 12px;
  padding: 0;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 4px 18px;
}
.other-files li { font-size: 13px; }
.other-files a {
  color: #aaa;
  text-decoration: none;
  font-family: 'Courier New', Consolas, monospace;
}
.other-files a:hover { color: #5580ff; }
`

// funcs is the template.FuncMap used by RenderPage. Right now it just
// has a helper for stripping the leading dot from a filename so the
// <source type="video/..."> tag gets a clean mime subtype.
var galleryFuncs = template.FuncMap{
	"ext": func(name string) string {
		ext := filepath.Ext(name)
		return strings.TrimPrefix(ext, ".")
	},
	// thumb strips the file extension. Used to build the thumb URL
	// (e.g. "photo.jpg" → "photo"). The ".webp" suffix is appended
	// by the template literal.
	"thumb": func(name string) string {
		return strings.TrimSuffix(name, filepath.Ext(name))
	},
}

// RenderPage returns the rendered HTML for a gallery page, with the
// dark-themed style sheet inlined in the <head>. Templates are loaded
// fresh from the templates directory on every call so designers can
// iterate on the look without rebuilding Caddy.
//
// Templates can come from one of two places (checked in order):
//  1. The directory in the GALLERY_TEMPLATES_DIR env var
//  2. /etc/caddy/gallery-templates
//
// If neither directory has a gallery.tmpl file, the bundled
// galleryTemplate + styleCSS constants are used (so the module works
// out of the box without any template files on disk).
func RenderPage(title, pathPrefix, thumbPrefix string, files []FileInfo) (string, error) {
	data := PageData{
		Title:       title,
		PathPrefix:  pathPrefix,
		ThumbPrefix: thumbPrefix,
	}
	for _, f := range files {
		switch f.Kind {
		case KindImage:
			data.Images = append(data.Images, f)
		case KindVideo:
			data.Videos = append(data.Videos, f)
		default:
			data.OtherFiles = append(data.OtherFiles, f)
		}
	}

	tmpl, err := loadTemplate()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// loadTemplate returns a *template.Template for rendering the gallery.
// Tries on-disk templates first (for hot-iteration), falls back to
// the bundled constants. Bundled style is always available; on-disk
// templates may override it.
func loadTemplate() (*template.Template, error) {
	dir := os.Getenv("GALLERY_TEMPLATES_DIR")
	if dir == "" {
		dir = "/etc/caddy/gallery-templates"
	}
	tmplPath := filepath.Join(dir, "gallery.tmpl")
	// Single err var so the assignments below can use `=` instead of `:=`.
	var err error
	if _, statErr := os.Stat(tmplPath); statErr == nil {
		// Load from disk; we still need to provide the styleCSS constant
		// to the template so {{template "style.css" .}} resolves. We
		// use ParseFiles for the page template and then inject styleCSS
		// as a named template.
		t := template.New("gallery.tmpl").Funcs(galleryFuncs)
		t, err = t.ParseFiles(tmplPath)
		if err != nil {
			return nil, err
		}
		// Re-register style.css from the constant.
		t, err = t.New("style.css").Parse(styleCSS)
		if err != nil {
			return nil, err
		}
		return t, nil
	}
	// Fall back to bundled templates.
	t := template.New("gallery").Funcs(galleryFuncs)
	t, err = t.New("style.css").Parse(styleCSS)
	if err != nil {
		return nil, err
	}
	t, err = t.New("gallery").Parse(galleryTemplate)
	if err != nil {
		return nil, err
	}
	return t, nil
}

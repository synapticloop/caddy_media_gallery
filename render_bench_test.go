package gallery

import (
	"fmt"
	"net/url"
	"testing"
)

// BenchmarkRenderPage_60Files measures the time to render
// a gallery page with 60 image files (typical page size).
// This is the BASELINE for performance comparison.
func BenchmarkRenderPage_60Files(b *testing.B) {
	files := make([]FileInfo, 60)
	for i := 0; i < 60; i++ {
		files[i] = FileInfo{
			Name:    fmt.Sprintf("test_image_%c.jpg", 'a'+i%26),
			ModTime: int64(i * 1000),
			Size:    int64(1024 * (i + 1)),
			Kind:    KindImage,
			Width:   1920,
			Height:  1080,
		}
	}
	q := url.Values{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = RenderPage("Test", "./", "./_thumbs/", "", "", false, false, 60,
			[]string{"30", "60", "120", "all"}, files, q, defaultImageExts, defaultVideoExts,
			"", "", "substring", "00", "00", "00", "00")
	}
}

// BenchmarkRenderPage_60Files_WithExif measures render time
// with EXIF data on every file (worst case).
func BenchmarkRenderPage_60Files_WithExif(b *testing.B) {
	files := make([]FileInfo, 60)
	for i := 0; i < 60; i++ {
		files[i] = FileInfo{
			Name:    fmt.Sprintf("test_image_%c.jpg", 'a'+i%26),
			ModTime: int64(i * 1000),
			Size:    int64(1024 * (i + 1)),
			Kind:    KindImage,
			Width:   1920,
			Height:  1080,
			Exif: &ExifData{
				CameraMake:   "Canon",
				CameraModel:  "EOS R5",
				LensModel:    "RF 100mm F2.8 L MACRO IS USM",
				DateTaken:    "2024:10:15 09:23:17",
				ExposureTime: "1/200 s",
				Aperture:     "f/8",
				ISO:          "ISO 400",
				FocalLength:  "100 mm",
			},
		}
	}
	q := url.Values{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = RenderPage("Test", "./", "./_thumbs/", "", "", false, false, 60,
			[]string{"30", "60", "120", "all"}, files, q, defaultImageExts, defaultVideoExts,
			"", "", "substring", "00", "00", "00", "00")
	}
}

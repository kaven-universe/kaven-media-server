package imageproc

import (
	"errors"
	"testing"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		name      string
		header    []byte
		mimeType  string
		extension string
	}{
		{name: "JPEG", header: []byte{0xff, 0xd8, 0xff, 0xe0}, mimeType: "image/jpeg", extension: ".jpg"},
		{name: "PNG", header: []byte("\x89PNG\r\n\x1a\nrest"), mimeType: "image/png", extension: ".png"},
		{name: "GIF", header: []byte("GIF89a"), mimeType: "image/gif", extension: ".gif"},
		{name: "WebP", header: []byte("RIFF1234WEBP"), mimeType: "image/webp", extension: ".webp"},
		{name: "little endian TIFF", header: []byte{'I', 'I', 0x2a, 0}, mimeType: "image/tiff", extension: ".tiff"},
		{name: "big endian TIFF", header: []byte{'M', 'M', 0, 0x2a}, mimeType: "image/tiff", extension: ".tiff"},
		{name: "AVIF", header: []byte("\x00\x00\x00\x14ftypavif\x00\x00\x00\x00avif"), mimeType: "image/avif", extension: ".avif"},
		{name: "HEIF", header: []byte("\x00\x00\x00\x14ftypheic\x00\x00\x00\x00mif1"), mimeType: "image/heif", extension: ".heif"},
		{name: "SVG", header: []byte("\xef\xbb\xbf <?xml version=\"1.0\"?> <!-- fixture --> <svg xmlns=\"http://www.w3.org/2000/svg\"></svg>"), mimeType: "image/svg+xml", extension: ".svg"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			format, err := Detect(test.header)
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			if format.MIMEType != test.mimeType || format.Extension != test.extension {
				t.Fatalf("format = %#v, want %s %s", format, test.mimeType, test.extension)
			}
		})
	}
}

func TestDetectRejectsUnsupportedAndMisleadingContent(t *testing.T) {
	for _, header := range [][]byte{
		{},
		[]byte("not an image.jpg"),
		[]byte("<html><svg></svg></html>"),
		[]byte("RIFF1234NOPE"),
		[]byte("\x00\x00\x00\x18ftypxxxx\x00\x00\x00\x00xxxx"),
	} {
		if _, err := Detect(header); !errors.Is(err, ErrUnsupportedFormat) {
			t.Fatalf("Detect(%q) error = %v, want ErrUnsupportedFormat", header, err)
		}
	}
}

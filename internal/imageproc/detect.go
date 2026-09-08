package imageproc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

var ErrUnsupportedFormat = errors.New("unsupported image format")

type Format struct {
	MIMEType  string
	Extension string
}

func DetectFile(path string) (Format, error) {
	file, err := os.Open(path)
	if err != nil {
		return Format{}, fmt.Errorf("open image for format detection: %w", err)
	}
	defer file.Close()
	header, err := io.ReadAll(io.LimitReader(file, 4096))
	if err != nil {
		return Format{}, fmt.Errorf("read image header: %w", err)
	}
	return Detect(header)
}

func Detect(header []byte) (Format, error) {
	switch {
	case len(header) >= 3 && bytes.Equal(header[:3], []byte{0xff, 0xd8, 0xff}):
		return Format{MIMEType: "image/jpeg", Extension: ".jpg"}, nil
	case len(header) >= 8 && bytes.Equal(header[:8], []byte("\x89PNG\r\n\x1a\n")):
		return Format{MIMEType: "image/png", Extension: ".png"}, nil
	case len(header) >= 6 && (bytes.Equal(header[:6], []byte("GIF87a")) || bytes.Equal(header[:6], []byte("GIF89a"))):
		return Format{MIMEType: "image/gif", Extension: ".gif"}, nil
	case len(header) >= 12 && bytes.Equal(header[:4], []byte("RIFF")) && bytes.Equal(header[8:12], []byte("WEBP")):
		return Format{MIMEType: "image/webp", Extension: ".webp"}, nil
	case len(header) >= 4 && (bytes.Equal(header[:4], []byte{'I', 'I', 0x2a, 0}) || bytes.Equal(header[:4], []byte{'M', 'M', 0, 0x2a})):
		return Format{MIMEType: "image/tiff", Extension: ".tiff"}, nil
	}
	if format, ok := detectISOBaseMedia(header); ok {
		return format, nil
	}
	if looksLikeSVG(header) {
		return Format{MIMEType: "image/svg+xml", Extension: ".svg"}, nil
	}
	return Format{}, ErrUnsupportedFormat
}

func detectISOBaseMedia(header []byte) (Format, bool) {
	if len(header) < 16 || !bytes.Equal(header[4:8], []byte("ftyp")) {
		return Format{}, false
	}
	boxSize := int(binary.BigEndian.Uint32(header[:4]))
	if boxSize < 16 || boxSize > len(header) {
		return Format{}, false
	}
	brands := make([]string, 0, 1+(boxSize-16)/4)
	brands = append(brands, string(header[8:12]))
	for offset := 16; offset+4 <= boxSize; offset += 4 {
		brands = append(brands, string(header[offset:offset+4]))
	}
	for _, brand := range brands {
		if brand == "avif" || brand == "avis" {
			return Format{MIMEType: "image/avif", Extension: ".avif"}, true
		}
	}
	for _, brand := range brands {
		switch brand {
		case "heic", "heix", "hevc", "hevx", "heim", "heis", "mif1", "msf1":
			return Format{MIMEType: "image/heif", Extension: ".heif"}, true
		}
	}
	return Format{}, false
}

func looksLikeSVG(header []byte) bool {
	trimmed := bytes.TrimPrefix(header, []byte{0xef, 0xbb, 0xbf})
	text := strings.TrimSpace(strings.ToLower(string(trimmed)))
	if strings.HasPrefix(text, "<?xml") {
		end := strings.Index(text, "?>")
		if end < 0 {
			return false
		}
		text = strings.TrimSpace(text[end+2:])
	}
	for strings.HasPrefix(text, "<!--") {
		end := strings.Index(text, "-->")
		if end < 0 {
			return false
		}
		text = strings.TrimSpace(text[end+3:])
	}
	return strings.HasPrefix(text, "<svg") && (len(text) == 4 || strings.ContainsAny(text[4:5], " \t\r\n>"))
}

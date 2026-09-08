//go:build libvips && cgo

package imageproc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/davidbyttow/govips/v2/vips"
)

func TestLibvipsCodecMatrix(t *testing.T) {
	processor, err := NewDefaultProcessor(2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(processor.Close)

	tests := []struct {
		format    vips.ImageType
		extension string
		wantMIME  string
	}{
		{format: vips.ImageTypeJPEG, extension: ".jpg", wantMIME: "image/jpeg"},
		{format: vips.ImageTypePNG, extension: ".png", wantMIME: "image/png"},
		{format: vips.ImageTypeWEBP, extension: ".webp", wantMIME: "image/webp"},
		{format: vips.ImageTypeGIF, extension: ".gif", wantMIME: "image/gif"},
		{format: vips.ImageTypeTIFF, extension: ".tiff", wantMIME: "image/tiff"},
		{format: vips.ImageTypeAVIF, extension: ".avif", wantMIME: "image/avif"},
		{format: vips.ImageTypeHEIF, extension: ".heic", wantMIME: "image/heif"},
	}
	for _, test := range tests {
		t.Run(test.extension, func(t *testing.T) {
			encoded := encodeLibvipsFixture(t, test.format)
			filePath := filepath.Join(t.TempDir(), "source"+test.extension)
			if err := os.WriteFile(filePath, encoded, 0o600); err != nil {
				t.Fatal(err)
			}
			metadata, err := processor.Metadata(context.Background(), filePath)
			if err != nil || metadata.Width != 2 || metadata.Height != 2 {
				t.Fatalf("metadata = %#v, error = %v", metadata, err)
			}
			result, err := processor.Transform(context.Background(), filePath, Options{Width: 1, Height: 1, Quality: 75})
			if err != nil {
				t.Fatal(err)
			}
			if result.MIMEType != test.wantMIME || result.Width != 1 || result.Height != 1 || len(result.Bytes) == 0 {
				t.Fatalf("result = %#v, want MIME %q and 1x1 bytes", result, test.wantMIME)
			}
			assertLibvipsOutput(t, result.Bytes, test.format)
		})
	}

	t.Run("svg rasterizes to png", func(t *testing.T) {
		filePath := filepath.Join(t.TempDir(), "source.svg")
		if err := os.WriteFile(filePath, libvipsTestSVG, 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := processor.Transform(context.Background(), filePath, Options{Width: 1})
		if err != nil {
			t.Fatal(err)
		}
		if result.MIMEType != "image/png" || result.Width != 1 {
			t.Fatalf("result = %#v, want PNG width 1", result)
		}
		assertLibvipsOutput(t, result.Bytes, vips.ImageTypePNG)
	})

	t.Run("animated gif matches legacy first-frame behavior", func(t *testing.T) {
		contract := loadAnimationContract(t)
		filePath := filepath.Join(t.TempDir(), "animated.gif")
		if err := os.WriteFile(filePath, contract.Source, 0o600); err != nil {
			t.Fatal(err)
		}
		metadata, err := processor.Metadata(context.Background(), filePath)
		if err != nil {
			t.Fatal(err)
		}
		if metadata.Width != contract.Input.Width || metadata.Height != contract.Input.PageHeight || metadata.Pages != contract.Input.Pages {
			t.Fatalf("source metadata = %#v, want %dx%d with %d pages", metadata, contract.Input.Width, contract.Input.PageHeight, contract.Input.Pages)
		}
		result, err := processor.Transform(context.Background(), filePath, Options{Width: contract.Transformed.Width})
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := vips.NewImageFromBuffer(result.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		defer decoded.Close()
		output := decoded.Metadata()
		if result.MIMEType != "image/gif" || result.Width != contract.Transformed.Width || result.Height != contract.Transformed.Height || output.Pages != contract.Transformed.Pages {
			t.Fatalf("transformed result = %#v, metadata = %#v, want %dx%d GIF with %d page", result, output, contract.Transformed.Width, contract.Transformed.Height, contract.Transformed.Pages)
		}
	})
}

func assertLibvipsOutput(t *testing.T, data []byte, format vips.ImageType) {
	t.Helper()
	decoded, err := vips.NewImageFromBuffer(data)
	if err != nil {
		t.Fatalf("decode transformed output: %v", err)
	}
	defer decoded.Close()
	metadata := decoded.Metadata()
	if metadata.Width != 1 || metadata.Height != 1 || metadata.Format != format {
		t.Fatalf("transformed output metadata = %#v, want 1x1 %s", metadata, vips.ImageTypes[format])
	}
	// Export forces pixel decoding; opening alone can defer codec failures.
	if _, _, err := decoded.ExportPng(vips.NewPngExportParams()); err != nil {
		t.Fatalf("decode transformed pixels: %v", err)
	}
}

var libvipsTestSVG = []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="2" height="2"><rect width="2" height="2" fill="red"/></svg>`)

func encodeLibvipsFixture(t *testing.T, format vips.ImageType) []byte {
	t.Helper()
	image, err := vips.NewImageFromBuffer(libvipsTestSVG)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	var data []byte
	switch format {
	case vips.ImageTypeJPEG:
		data, _, err = image.ExportJpeg(vips.NewJpegExportParams())
	case vips.ImageTypePNG:
		data, _, err = image.ExportPng(vips.NewPngExportParams())
	case vips.ImageTypeWEBP:
		data, _, err = image.ExportWebp(vips.NewWebpExportParams())
	case vips.ImageTypeGIF:
		data, _, err = image.ExportGIF(vips.NewGifExportParams())
	case vips.ImageTypeTIFF:
		data, _, err = image.ExportTiff(vips.NewTiffExportParams())
	case vips.ImageTypeAVIF:
		data, _, err = image.ExportAvif(vips.NewAvifExportParams())
	case vips.ImageTypeHEIF:
		data, _, err = image.ExportHeif(vips.NewHeifExportParams())
	default:
		err = fmt.Errorf("unsupported fixture format %d", format)
	}
	if err != nil {
		t.Fatalf("encode %s fixture: %v", vips.ImageTypes[format], err)
	}
	return data
}

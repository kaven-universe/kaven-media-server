//go:build libvips && cgo

package imageproc

import (
	"context"
	"fmt"

	"github.com/davidbyttow/govips/v2/vips"
)

type vipsBackend struct{}

func NewDefaultProcessor(maxConcurrent int) (*Processor, error) {
	if err := vips.Startup(nil); err != nil {
		return nil, fmt.Errorf("start libvips: %w", err)
	}
	processor, err := NewProcessor(vipsBackend{}, maxConcurrent)
	if err != nil {
		vips.Shutdown()
		return nil, err
	}
	return processor, nil
}

func (vipsBackend) Metadata(ctx context.Context, filePath string) (Metadata, error) {
	if err := ctx.Err(); err != nil {
		return Metadata{}, err
	}
	image, err := loadImage(filePath)
	if err != nil {
		return Metadata{}, err
	}
	defer image.Close()
	metadata := image.Metadata()
	return Metadata{
		Width: metadata.Width, Height: metadata.Height, Pages: metadata.Pages,
		Format: vips.ImageTypes[image.OriginalFormat()],
	}, nil
}

func (vipsBackend) Transform(ctx context.Context, filePath string, options Options) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	image, err := loadImage(filePath)
	if err != nil {
		return Result{}, err
	}
	defer image.Close()

	format := image.OriginalFormat()
	if options.Width > 0 || options.Height > 0 {
		width, height := options.Width, options.Height
		if width == 0 {
			width = -1
		}
		if height == 0 {
			height = -1
		}
		if err := image.ThumbnailWithSize(width, height, vips.InterestingCentre, vips.SizeBoth); err != nil {
			return Result{}, fmt.Errorf("resize image: %w", err)
		}
	}

	data, mimeType, err := exportImage(image, format, options.Quality)
	if err != nil {
		return Result{}, err
	}
	return Result{Bytes: data, MIMEType: mimeType, Width: image.Width(), Height: image.Height()}, nil
}

func (vipsBackend) Close() { vips.Shutdown() }

func loadImage(filePath string) (*vips.ImageRef, error) {
	params := vips.NewImportParams()
	params.FailOnError.Set(false)
	image, err := vips.LoadImageFromFile(filePath, params)
	if err != nil {
		return nil, fmt.Errorf("%w: decode image: %v", ErrUnsupported, err)
	}
	return image, nil
}

func exportImage(image *vips.ImageRef, format vips.ImageType, quality int) ([]byte, string, error) {
	var (
		data []byte
		err  error
	)
	switch format {
	case vips.ImageTypeJPEG:
		params := vips.NewJpegExportParams()
		setQuality(&params.Quality, quality)
		data, _, err = image.ExportJpeg(params)
	case vips.ImageTypePNG:
		data, _, err = image.ExportPng(vips.NewPngExportParams())
	case vips.ImageTypeWEBP:
		params := vips.NewWebpExportParams()
		setQuality(&params.Quality, quality)
		data, _, err = image.ExportWebp(params)
	case vips.ImageTypeGIF:
		data, _, err = image.ExportGIF(vips.NewGifExportParams())
	case vips.ImageTypeTIFF:
		params := vips.NewTiffExportParams()
		setQuality(&params.Quality, quality)
		data, _, err = image.ExportTiff(params)
	case vips.ImageTypeHEIF:
		params := vips.NewHeifExportParams()
		setQuality(&params.Quality, quality)
		data, _, err = image.ExportHeif(params)
	case vips.ImageTypeAVIF:
		params := vips.NewAvifExportParams()
		setQuality(&params.Quality, quality)
		data, _, err = image.ExportAvif(params)
	case vips.ImageTypeSVG:
		data, _, err = image.ExportPng(vips.NewPngExportParams())
		format = vips.ImageTypePNG
	default:
		return nil, "", fmt.Errorf("%w: %s", ErrUnsupported, vips.ImageTypes[format])
	}
	if err != nil {
		return nil, "", fmt.Errorf("encode %s: %w", vips.ImageTypes[format], err)
	}
	return data, mimeForVipsType(format), nil
}

func setQuality(target *int, quality int) {
	if quality > 0 {
		*target = quality
	}
}

func mimeForVipsType(format vips.ImageType) string {
	switch format {
	case vips.ImageTypeJPEG:
		return "image/jpeg"
	case vips.ImageTypePNG:
		return "image/png"
	case vips.ImageTypeWEBP:
		return "image/webp"
	case vips.ImageTypeGIF:
		return "image/gif"
	case vips.ImageTypeTIFF:
		return "image/tiff"
	case vips.ImageTypeAVIF:
		return "image/avif"
	case vips.ImageTypeHEIF:
		return "image/heif"
	default:
		return "application/octet-stream"
	}
}

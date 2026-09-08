package imageproc

import (
	"context"
	"errors"
	"fmt"
)

const (
	MaxDimension = 10_000
	MaxPixels    = 100_000_000
)

var (
	ErrUnavailable = errors.New("image processing is unavailable")
	ErrInvalid     = errors.New("invalid image transformation")
	ErrUnsupported = errors.New("unsupported image format")
)

type Options struct {
	Width   int
	Height  int
	Quality int
}

type Metadata struct {
	Width  int
	Height int
	Pages  int
	Format string
}

type Result struct {
	Bytes    []byte
	MIMEType string
	Width    int
	Height   int
}

type Backend interface {
	Metadata(context.Context, string) (Metadata, error)
	Transform(context.Context, string, Options) (Result, error)
	Close()
}

type Processor struct {
	backend Backend
	slots   chan struct{}
}

func NewProcessor(backend Backend, maxConcurrent int) (*Processor, error) {
	if backend == nil {
		return nil, fmt.Errorf("create image processor: nil backend")
	}
	if maxConcurrent < 1 {
		return nil, fmt.Errorf("create image processor: concurrency must be positive")
	}
	return &Processor{backend: backend, slots: make(chan struct{}, maxConcurrent)}, nil
}

func (processor *Processor) Metadata(ctx context.Context, filePath string) (Metadata, error) {
	if err := processor.acquire(ctx); err != nil {
		return Metadata{}, err
	}
	defer processor.release()
	return processor.backend.Metadata(ctx, filePath)
}

func (processor *Processor) Transform(ctx context.Context, filePath string, options Options) (Result, error) {
	if err := validateOptions(options); err != nil {
		return Result{}, err
	}
	if err := processor.acquire(ctx); err != nil {
		return Result{}, err
	}
	defer processor.release()

	metadata, err := processor.backend.Metadata(ctx, filePath)
	if err != nil {
		return Result{}, err
	}
	if err := validateOutputSize(metadata, options); err != nil {
		return Result{}, err
	}
	return processor.backend.Transform(ctx, filePath, options)
}

func (processor *Processor) Close() {
	processor.backend.Close()
}

func (processor *Processor) acquire(ctx context.Context) error {
	select {
	case processor.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (processor *Processor) release() {
	<-processor.slots
}

func validateOptions(options Options) error {
	if options.Width == 0 && options.Height == 0 && options.Quality == 0 {
		return fmt.Errorf("%w: no options", ErrInvalid)
	}
	if options.Width < 0 || options.Width > MaxDimension || options.Height < 0 || options.Height > MaxDimension {
		return fmt.Errorf("%w: dimensions must be between 1 and %d", ErrInvalid, MaxDimension)
	}
	if options.Quality < 0 || options.Quality > 100 {
		return fmt.Errorf("%w: quality must be between 1 and 100", ErrInvalid)
	}
	return nil
}

func validateOutputSize(metadata Metadata, options Options) error {
	if metadata.Width < 1 || metadata.Height < 1 {
		return fmt.Errorf("%w: invalid source dimensions", ErrUnsupported)
	}
	width, height := options.Width, options.Height
	if width == 0 && height == 0 {
		width, height = metadata.Width, metadata.Height
	} else if width == 0 {
		width = int((int64(metadata.Width)*int64(height) + int64(metadata.Height) - 1) / int64(metadata.Height))
	} else if height == 0 {
		height = int((int64(metadata.Height)*int64(width) + int64(metadata.Width) - 1) / int64(metadata.Width))
	}
	if width > MaxDimension || height > MaxDimension {
		return fmt.Errorf("%w: output dimensions exceed %d", ErrInvalid, MaxDimension)
	}
	if int64(width)*int64(height) > MaxPixels {
		return fmt.Errorf("%w: output exceeds %d pixels", ErrInvalid, MaxPixels)
	}
	return nil
}

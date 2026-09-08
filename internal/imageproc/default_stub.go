//go:build !libvips || !cgo

package imageproc

import "context"

type unavailableBackend struct{}

func NewDefaultProcessor(maxConcurrent int) (*Processor, error) {
	return NewProcessor(unavailableBackend{}, maxConcurrent)
}

func (unavailableBackend) Metadata(context.Context, string) (Metadata, error) {
	return Metadata{}, ErrUnavailable
}

func (unavailableBackend) Transform(context.Context, string, Options) (Result, error) {
	return Result{}, ErrUnavailable
}

func (unavailableBackend) Close() {}

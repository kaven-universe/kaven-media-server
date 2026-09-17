package imageproc

import (
	"context"
	"errors"
	"testing"
)

type testBackend struct {
	metadata Metadata
	calls    int
}

func (backend *testBackend) Metadata(context.Context, string) (Metadata, error) {
	return backend.metadata, nil
}

func (backend *testBackend) Transform(_ context.Context, _ string, options Options) (Result, error) {
	backend.calls++
	return Result{MIMEType: "image/jpeg", Width: options.Width, Height: options.Height}, nil
}

func (*testBackend) Close() {}

func TestProcessorValidatesTransformLimits(t *testing.T) {
	tests := []struct {
		name     string
		metadata Metadata
		options  Options
	}{
		{name: "empty", metadata: Metadata{Width: 10, Height: 10}},
		{name: "negative width", metadata: Metadata{Width: 10, Height: 10}, options: Options{Width: -1}},
		{name: "dimension too large", metadata: Metadata{Width: 10, Height: 10}, options: Options{Width: MaxDimension + 1}},
		{name: "quality too large", metadata: Metadata{Width: 10, Height: 10}, options: Options{Quality: 101}},
		{name: "derived dimension too large", metadata: Metadata{Width: 20_000, Height: 40_000}, options: Options{Width: MaxDimension}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &testBackend{metadata: test.metadata}
			processor, err := NewProcessor(backend, 1)
			if err != nil {
				t.Fatal(err)
			}
			_, err = processor.Transform(context.Background(), "image", test.options)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
			if backend.calls != 0 {
				t.Fatalf("backend transforms = %d, want 0", backend.calls)
			}
		})
	}
}

func TestProcessorAcceptsBoundedTransform(t *testing.T) {
	backend := &testBackend{metadata: Metadata{Width: 400, Height: 200}}
	processor, err := NewProcessor(backend, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.Transform(context.Background(), "image", Options{Width: 100, Quality: 85}); err != nil {
		t.Fatal(err)
	}
	if backend.calls != 1 {
		t.Fatalf("backend transforms = %d, want 1", backend.calls)
	}
}

func TestProcessorBoundsConcurrencyAndHonorsCancellation(t *testing.T) {
	backend := &blockingBackend{started: make(chan struct{}), release: make(chan struct{})}
	processor, err := NewProcessor(backend, 1)
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, err := processor.Transform(context.Background(), "first", Options{Width: 1})
		firstDone <- err
	}()
	<-backend.started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := processor.Transform(ctx, "second", Options{Width: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting transform error = %v, want context canceled", err)
	}
	close(backend.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first transform: %v", err)
	}
}

type blockingBackend struct {
	started chan struct{}
	release chan struct{}
}

func (*blockingBackend) Metadata(context.Context, string) (Metadata, error) {
	return Metadata{Width: 10, Height: 10}, nil
}

func (backend *blockingBackend) Transform(context.Context, string, Options) (Result, error) {
	close(backend.started)
	<-backend.release
	return Result{}, nil
}

func (*blockingBackend) Close() {}

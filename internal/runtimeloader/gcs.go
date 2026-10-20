package runtimeloader

import (
	"context"
	"fmt"
	"io"
	"time"

	"cloud.google.com/go/storage"
)

const defaultGCSDeadline = 8 * time.Second

// ObjectReader читает один generation-pinned object. Реализация не
// используется на request path.
type ObjectReader interface {
	ReadObject(ctx context.Context, pin ObjectPin) ([]byte, error)
}

// LatestReader читает текущую generation объекта без pin. Не на
// request path. Artifact pins по-прежнему только через ReadObject.
type LatestReader interface {
	ReadLatest(ctx context.Context, bucket, object string) (LatestObject, error)
}

// LatestObject — непропинненное чтение pointer object. SHA/size здесь
// наблюдаемые, не compiled trust anchor.
type LatestObject struct {
	Data       []byte
	Generation int64
	Size       int
	SHA256     string
}

// GCSReader — ADC Storage client с лимитом размера и deadline.
type GCSReader struct {
	client   *storage.Client
	deadline time.Duration
}

func NewGCSReader(ctx context.Context) (*GCSReader, error) {
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("storage client: %w", err)
	}
	return &GCSReader{client: client, deadline: defaultGCSDeadline}, nil
}

func (r *GCSReader) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}

func (r *GCSReader) ReadObject(ctx context.Context, pin ObjectPin) ([]byte, error) {
	if pin.Generation <= 0 {
		return nil, fmt.Errorf("generation is required for %s", pin.Object)
	}
	if pin.Size <= 0 || pin.Size > maxArtifactBytes {
		return nil, fmt.Errorf("pinned size %d out of bounds for %s", pin.Size, pin.Object)
	}
	deadline := r.deadline
	if deadline <= 0 {
		deadline = defaultGCSDeadline
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	obj := r.client.Bucket(pin.Bucket).Object(pin.Object).Generation(pin.Generation)
	rd, err := obj.NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcs read %s#%d: %w", pin.Object, pin.Generation, err)
	}
	defer rd.Close()
	if rd.Attrs.Generation != pin.Generation {
		return nil, fmt.Errorf("gcs generation mismatch %s: got %d want %d", pin.Object, rd.Attrs.Generation, pin.Generation)
	}
	if rd.Attrs.Size != int64(pin.Size) {
		return nil, fmt.Errorf("gcs size mismatch %s: got %d want %d", pin.Object, rd.Attrs.Size, pin.Size)
	}
	data, err := io.ReadAll(io.LimitReader(rd, int64(pin.Size)+1))
	if err != nil {
		return nil, fmt.Errorf("gcs body %s: %w", pin.Object, err)
	}
	if len(data) != pin.Size {
		return nil, fmt.Errorf("gcs body size %s: got %d want %d", pin.Object, len(data), pin.Size)
	}
	if sha256Hex(data) != pin.SHA256 {
		return nil, fmt.Errorf("gcs sha256 mismatch %s", pin.Object)
	}
	return data, nil
}

func (r *GCSReader) ReadLatest(ctx context.Context, bucket, object string) (LatestObject, error) {
	if bucket == "" || object == "" {
		return LatestObject{}, fmt.Errorf("gcs latest: bucket and object are required")
	}
	deadline := r.deadline
	if deadline <= 0 {
		deadline = defaultGCSDeadline
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	rd, err := r.client.Bucket(bucket).Object(object).NewReader(ctx)
	if err != nil {
		return LatestObject{}, fmt.Errorf("gcs latest %s: %w", object, err)
	}
	defer rd.Close()
	if rd.Attrs.Generation <= 0 {
		return LatestObject{}, fmt.Errorf("gcs latest %s: missing generation", object)
	}
	if rd.Attrs.Size > maxArtifactBytes {
		return LatestObject{}, fmt.Errorf("gcs latest %s: size %d exceeds %d", object, rd.Attrs.Size, maxArtifactBytes)
	}
	data, err := io.ReadAll(io.LimitReader(rd, int64(maxArtifactBytes)+1))
	if err != nil {
		return LatestObject{}, fmt.Errorf("gcs latest body %s: %w", object, err)
	}
	if len(data) == 0 || len(data) > maxArtifactBytes {
		return LatestObject{}, fmt.Errorf("gcs latest body %s: size %d out of bounds", object, len(data))
	}
	return LatestObject{
		Data:       data,
		Generation: rd.Attrs.Generation,
		Size:       len(data),
		SHA256:     sha256Hex(data),
	}, nil
}

func LoadPins(ctx context.Context, r ObjectReader, pins GCSPins) (*Bundle, error) {
	if r == nil {
		return nil, fmt.Errorf("nil object reader")
	}
	rel, err := r.ReadObject(ctx, pins.Release)
	if err != nil {
		return nil, err
	}
	prog, err := r.ReadObject(ctx, pins.Program)
	if err != nil {
		return nil, err
	}
	w, err := r.ReadObject(ctx, pins.Weights)
	if err != nil {
		return nil, err
	}
	return Verify(rel, prog, w)
}

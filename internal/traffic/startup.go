package traffic

import (
	"context"
	"fmt"

	"ta-events-ingest/internal/runtimeloader"
)

// Startup is the load-path result: a prepared contract plus overlay errors
// that were absorbed into last-known-good. A nil Contract is a cold instance
// without LKG — /ready must stay 503.
type Startup struct {
	Contract *Contract
	KeptLKG  bool
	Errs     []error
}

// StartupInput is the load-path inputs. GCS == nil with ArtifactSource=gcs
// is a reader/outage: keep dir LKG, do not swap.
type StartupInput struct {
	PinnedDir      string
	ArtifactSource string
	GCS            runtimeloader.ObjectReader
	Pins           runtimeloader.GCSPins
	PinErr         error
	UsePointer     bool
	Pointer        runtimeloader.ObjectPin
	PointerErr     error
	LoadDir        func(string) (*runtimeloader.Bundle, error)
	LoadPins       func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error)
	LoadPointer    func(context.Context, runtimeloader.ObjectReader, runtimeloader.ObjectPin) (*runtimeloader.Pointer, error)
}

// LoadStartup loads the revision-pinned directory first (LKG), then optionally
// overlays generation-pinned GCS. When UsePointer is set, pins come from the
// producer pointer object (activation=gcs-pointer); a pointer failure must
// not fall through to compiled C1 pins. Missing/corrupt GCS, a nil reader, or
// an incompatible executor keep LKG. Only a broken PinnedDir is fatal.
func LoadStartup(ctx context.Context, in StartupInput) (*Startup, error) {
	loadDir := in.LoadDir
	if loadDir == nil {
		loadDir = runtimeloader.LoadDir
	}
	loadPins := in.LoadPins
	if loadPins == nil {
		loadPins = runtimeloader.LoadPins
	}

	out := &Startup{}
	if in.PinnedDir != "" {
		b, err := loadDir(in.PinnedDir)
		if err != nil {
			return nil, fmt.Errorf("PINNED_DIR: %w", err)
		}
		c, err := NewContract(b, ActivationRevisionPinned)
		if err != nil {
			return nil, fmt.Errorf("PINNED_DIR prepare: %w", err)
		}
		out.Contract = c
	}

	if in.ArtifactSource != artifactSourceGCS {
		return out, nil
	}
	if in.UsePointer {
		return overlayPointer(ctx, in, out, loadPins)
	}
	return overlayPins(ctx, in, out, loadPins)
}

func overlayKeep(out *Startup, err error) (*Startup, error) {
	out.KeptLKG = out.Contract != nil
	out.Errs = append(out.Errs, err)
	return out, nil
}

func overlayPointer(ctx context.Context, in StartupInput, out *Startup, loadPins func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error)) (*Startup, error) {
	if in.PointerErr != nil {
		return overlayKeep(out, fmt.Errorf("gcs pointer: %w", in.PointerErr))
	}
	if in.GCS == nil {
		return overlayKeep(out, fmt.Errorf("gcs reader unavailable; keeping LKG"))
	}
	loadPointer := in.LoadPointer
	if loadPointer == nil {
		loadPointer = runtimeloader.LoadPointerPins
	}
	ptr, err := loadPointer(ctx, in.GCS, in.Pointer)
	if err != nil {
		return overlayKeep(out, fmt.Errorf("gcs pointer: %w", err))
	}
	pins, err := runtimeloader.PinsFromPointer(ptr)
	if err != nil {
		return overlayKeep(out, fmt.Errorf("gcs pointer: %w", err))
	}
	b, err := loadPins(ctx, in.GCS, pins)
	if err != nil {
		return overlayKeep(out, fmt.Errorf("gcs pointer pins: %w", err))
	}
	c, err := NewContract(b, ActivationGCSPointer)
	if err != nil {
		return overlayKeep(out, fmt.Errorf("gcs pointer overlay incompatible: %w", err))
	}
	c.attachPointer(ptr.Version, in.Pointer.Generation, ptr.ArtifactSetID)
	out.Contract = c
	return out, nil
}

func overlayPins(ctx context.Context, in StartupInput, out *Startup, loadPins func(context.Context, runtimeloader.ObjectReader, runtimeloader.GCSPins) (*runtimeloader.Bundle, error)) (*Startup, error) {
	if in.PinErr != nil {
		return overlayKeep(out, fmt.Errorf("gcs pins: %w", in.PinErr))
	}
	if in.GCS == nil {
		return overlayKeep(out, fmt.Errorf("gcs reader unavailable; keeping LKG"))
	}
	b, err := loadPins(ctx, in.GCS, in.Pins)
	if err != nil {
		return overlayKeep(out, fmt.Errorf("gcs pins: %w", err))
	}
	c, err := NewContract(b, ActivationGCSGeneration)
	if err != nil {
		return overlayKeep(out, fmt.Errorf("gcs overlay incompatible: %w", err))
	}
	out.Contract = c
	return out, nil
}

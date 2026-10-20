package runtimeloader

import (
	"fmt"
	"os"
	"strconv"
)

// Compiled default pins for local/tests. Production must override the
// bucket (and generations) via TRAFFIC_PIN_* / TRAFFIC_POINTER_*.
// Never resolve live/latest aliases; generation is required.
const (
	PinBucket        = "example-compiled-artifacts"
	PinCompilerJob   = "999c8d38-6852-4d0e-8e3f-30cdb68868a9"
	PinArtifactSet   = "59e20642-0d5d-47e1-ac55-80730d8ccaa3"
	PinProgramSHA256 = "53bfd83f26c6e7d3bc434236b51d1d618bf10b0e498ceb622eda52e7c05ae8d3"
	PinProgramGen    = 1789081598223417
	PinProgramSize   = 5589
	PinWeightsSHA256 = "8cc0537da05eed40ae702585373714bbaa1f6849a6cee9e8761aacde06fe3a7f"
	PinWeightsGen    = 1789081598353827
	PinWeightsSize   = 977
	PinReleaseSHA256 = "e6f73084ed2b112effccdf9461c7a2cfdb81bf97dd1349137d628c242427d70c"
	PinReleaseGen    = 1789081598443957
	PinReleaseSize   = 850
)

const ContractSmokeURLMarker = "campaign=contract-smoke"

type ObjectPin struct {
	Bucket     string
	Object     string
	Generation int64
	Size       int
	SHA256     string
}

// GCSPins — ровно три generation-pinned объекта. Четвёртый pin
// redirect-targets / target registry отклонён и не добавлять.
type GCSPins struct {
	Program ObjectPin
	Weights ObjectPin
	Release ObjectPin
}

func ArtifactObject(kind, sha string) string {
	return fmt.Sprintf("traffic-runtime/v1/%s/sha256/%s.json", kind, sha)
}

func DefaultPins() GCSPins {
	return GCSPins{
		Program: ObjectPin{
			Bucket:     PinBucket,
			Object:     ArtifactObject("program", PinProgramSHA256),
			Generation: PinProgramGen,
			Size:       PinProgramSize,
			SHA256:     PinProgramSHA256,
		},
		Weights: ObjectPin{
			Bucket:     PinBucket,
			Object:     ArtifactObject("weights", PinWeightsSHA256),
			Generation: PinWeightsGen,
			Size:       PinWeightsSize,
			SHA256:     PinWeightsSHA256,
		},
		Release: ObjectPin{
			Bucket:     PinBucket,
			Object:     ArtifactObject("release", PinReleaseSHA256),
			Generation: PinReleaseGen,
			Size:       PinReleaseSize,
			SHA256:     PinReleaseSHA256,
		},
	}
}

// PinsFromEnv starts from DefaultPins and applies TRAFFIC_PIN_* overrides.
// Used by T3/T5 probe revisions (missing/corrupt generation or sha) without
// changing the compiled C1 defaults. Empty env leaves C1 pins unchanged.
// A set but malformed/zero/negative GENERATION or SIZE is an error: do not
// fall back to the compiled C1 pin (fail-closed exact generation pinning).
func PinsFromEnv() (GCSPins, error) {
	p := DefaultPins()
	if b := os.Getenv("TRAFFIC_PIN_BUCKET"); b != "" {
		p.Program.Bucket = b
		p.Weights.Bucket = b
		p.Release.Bucket = b
	}
	if err := overridePin(&p.Program, "PROGRAM"); err != nil {
		return GCSPins{}, err
	}
	if err := overridePin(&p.Weights, "WEIGHTS"); err != nil {
		return GCSPins{}, err
	}
	if err := overridePin(&p.Release, "RELEASE"); err != nil {
		return GCSPins{}, err
	}
	return p, nil
}

func overridePin(pin *ObjectPin, kind string) error {
	if v := os.Getenv("TRAFFIC_PIN_" + kind + "_SHA256"); v != "" {
		pin.SHA256 = v
		if os.Getenv("TRAFFIC_PIN_"+kind+"_OBJECT") == "" {
			pin.Object = ArtifactObject(kindToPath(kind), v)
		}
	}
	if v := os.Getenv("TRAFFIC_PIN_" + kind + "_OBJECT"); v != "" {
		pin.Object = v
	}
	if v := os.Getenv("TRAFFIC_PIN_" + kind + "_GENERATION"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("TRAFFIC_PIN_%s_GENERATION %q: %w", kind, v, err)
		}
		if n <= 0 {
			return fmt.Errorf("TRAFFIC_PIN_%s_GENERATION must be > 0, got %d", kind, n)
		}
		pin.Generation = n
	}
	if v := os.Getenv("TRAFFIC_PIN_" + kind + "_SIZE"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("TRAFFIC_PIN_%s_SIZE %q: %w", kind, v, err)
		}
		if n <= 0 || n > int64(maxArtifactBytes) {
			return fmt.Errorf("TRAFFIC_PIN_%s_SIZE must be in 1..%d, got %d", kind, maxArtifactBytes, n)
		}
		pin.Size = int(n)
	}
	return nil
}

func kindToPath(kind string) string {
	switch kind {
	case "PROGRAM":
		return "program"
	case "WEIGHTS":
		return "weights"
	case "RELEASE":
		return "release"
	default:
		return kind
	}
}

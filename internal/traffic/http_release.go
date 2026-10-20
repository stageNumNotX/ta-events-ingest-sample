package traffic

import (
	"fmt"
	"strings"

	"ta-events-ingest/internal/decision"
	"ta-events-ingest/internal/runtimeloader"
	"ta-events-ingest/internal/trafficwire"
)

const (
	httpReleasePrefix      = "c1-payload-url"
	preferredVariantID     = "smartlink-main"
	contractSmokeURLMarker = "campaign=contract-smoke"
)

// HTTPReleaseFromBundle returns a PreparedRelease whose Target is an
// executable redirect-target-v2 payload.url from the verified bundle.
// Prefer variant smartlink-main, then any URL carrying the C1
// contract-smoke marker. Load-path evidence only: GET / must Resolve
// the selected variant and must not call this.
func HTTPReleaseFromBundle(bundle *runtimeloader.Bundle) (*PreparedRelease, error) {
	if bundle == nil || bundle.Program == nil {
		return nil, fmt.Errorf("nil bundle")
	}
	var preferred, smoke, any *PreparedRelease
	for _, v := range bundle.Program.Variants {
		if v.TypeID != trafficwire.TypeRedirectTarget {
			continue
		}
		pl, err := trafficwire.ParseRedirect(v.HandlerVersion, v.SchemaVersion, v.Payload)
		if err != nil {
			return nil, fmt.Errorf("variant %s: %w", v.ID, err)
		}
		url, err := decision.RedirectURL(pl)
		if err != nil {
			continue
		}
		rel := &PreparedRelease{Name: httpReleasePrefix + "/" + v.ID, Target: url}
		switch {
		case v.ID == preferredVariantID:
			preferred = rel
		case strings.Contains(url, contractSmokeURLMarker):
			smoke = rel
		default:
			if any == nil {
				any = rel
			}
		}
	}
	switch {
	case preferred != nil:
		return preferred, nil
	case smoke != nil:
		return smoke, nil
	case any != nil:
		return any, nil
	default:
		return nil, fmt.Errorf("bundle has no executable redirect-target-v2 payload.url")
	}
}

func (c *Contract) HTTPRelease() (*PreparedRelease, error) {
	if c == nil || c.bundle == nil {
		return nil, fmt.Errorf("nil contract bundle")
	}
	return HTTPReleaseFromBundle(c.bundle)
}

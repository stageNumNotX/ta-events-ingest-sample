package traffic

import (
	"testing"
)

// BenchmarkT9ResolveRedirect is the pure-resolver half of T9: no HTTP,
// GCS, or Pub/Sub. Live GET / uses this same ResolveRedirect path.
func BenchmarkT9ResolveRedirect(b *testing.B) {
	c, err := NewContract(twoExecutableBundle(b), ActivationRevisionPinned)
	if err != nil {
		b.Fatal(err)
	}
	ctx := PrototypeContext()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr, err := c.ResolveRedirect(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if rr.URL == "" {
			b.Fatal("empty URL")
		}
	}
}

func BenchmarkT9ResolveRedirectQueryV1(b *testing.B) {
	c, err := NewContract(twoExecutableBundle(b), ActivationRevisionPinned)
	if err != nil {
		b.Fatal(err)
	}
	ctx := QueryV1Context("bench-click", "network-a", "campaign-42")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr, err := c.ResolveRedirect(ctx)
		if err != nil {
			b.Fatal(err)
		}
		if rr.URL == "" {
			b.Fatal("empty URL")
		}
	}
}

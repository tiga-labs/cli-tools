package enrich

import "testing"

func TestProviderClientIsSharedInProduction(t *testing.T) {
	if ProviderTransport != nil {
		t.Skip("ProviderTransport is set")
	}
	if a, b := newProviderClient(), newProviderClient(); a != b {
		t.Fatalf("expected a shared client when ProviderTransport is nil")
	}
	if newProviderClient().GetClient().Jar != nil {
		t.Fatalf("shared client must not keep a cookie jar")
	}
}

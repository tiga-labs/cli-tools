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

func TestTruncateRunesKeepsUTF8Valid(t *testing.T) {
	if got := truncateRunes("Société Générale", 7); got != "Société" {
		t.Fatalf("truncateRunes = %q", got)
	}
	if got := truncateRunes("Acme", 75); got != "Acme" {
		t.Fatalf("short string changed: %q", got)
	}
}

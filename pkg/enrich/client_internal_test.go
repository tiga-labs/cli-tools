package enrich

import "testing"

func TestTruncateRunesKeepsUTF8Valid(t *testing.T) {
	if got := truncateRunes("Société Générale", 7); got != "Société" {
		t.Fatalf("truncateRunes = %q", got)
	}
	if got := truncateRunes("Acme", 75); got != "Acme" {
		t.Fatalf("short string changed: %q", got)
	}
}

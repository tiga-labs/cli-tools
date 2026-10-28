//go:build chromelive

package chromeattach

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveAboutBlank attaches to the real running Chrome, opens about:blank in the default
// profile, evaluates 1+1 and closes the tab. No network traffic. Someone must click Allow.
func TestLiveAboutBlank(t *testing.T) {
	if os.Getenv("CHROMELIVE") != "1" {
		t.Skip("set CHROMELIVE=1 (make test-chromelive)")
	}
	ctx := context.Background()
	b, err := Connect(ctx, Options{Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	tab, err := b.OpenTab(ctx, "about:blank")
	if err != nil {
		t.Fatal(err)
	}
	defer tab.Close()
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := tab.WaitLoaded(wctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tab.Eval(ctx, "1+1", &n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("1+1 = %d", n)
	}
	t.Log("tab open in the default profile; closing in 5s — note which profile window has it")
	time.Sleep(5 * time.Second)
}

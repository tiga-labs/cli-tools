package signals

import (
	"context"
	"net/http"
	"testing"
)

func TestStripValueHTML(t *testing.T) {
	for in, want := range map[string]string{
		"<span>Yes. <b>Stripe</b> R&amp;D</span>":           "Yes. Stripe R&D",
		"<span>A <i>small</i> <em>note</em><br>next</span>": "A small note\nnext",
		"a < b & c > d":          "a < b & c > d",
		"Use <script> carefully": "Use <script> carefully",
		"  plain  ":              "plain",
		"<SPAN>Upper</SPAN>":     "Upper",
	} {
		if got := stripValueHTML(in); got != want {
			t.Errorf("stripValueHTML(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRunGPTNoSearchStripsHTML(t *testing.T) {
	newStubOpenAI(t, map[string]func(int) *http.Response{"/responses": always(200, outputText(`{"output":"<span>Yes. <b>Acme</b> sells R&amp;D tools.</span>","found":true}`))})
	res := RunGPT(context.Background(), fundingSignal(t, ""), acme, noSearchOpts())
	if res.Status != StatusSuccess || res.Value != "Yes. Acme sells R&D tools." {
		t.Fatalf("res = %+v", res)
	}
}

func TestHiringSingularJob(t *testing.T) {
	hiringStub(t, map[string]string{"account executive": jobsJSON(job{"Account Executive", "Austin, TX"})})
	res := RunHiringForRole(context.Background(), hiringSignal(t, ""), acmeCo, liClient(), liNow)
	if res.Value != `As of Wednesday, October 28, 2026 Acme has 1 job with the term "Account Executive" listed on LinkedIn.` {
		t.Fatalf("value = %q", res.Value)
	}
}

func TestCacheLogicVersionBumpedForPlainText(t *testing.T) {
	if cacheLogicVersion != "2" {
		t.Fatalf("cacheLogicVersion = %q; S9 changed value formatting, so stored HTML answers must not be served", cacheLogicVersion)
	}
}

func TestStripValueHTMLKeepsProse(t *testing.T) {
	for in, want := range map[string]string{
		"Brand&copywriting, Sales&regional, x&notes": "Brand&copywriting, Sales&regional, x&notes",
		"AT&amp;T &#38; S&#x26;P &copy;":             "AT&T & S&P ©",
		`<span class="x">Attributed</span>`:          "Attributed",
	} {
		if got := stripValueHTML(in); got != want {
			t.Errorf("stripValueHTML(%q) = %q, want %q", in, got, want)
		}
	}
}

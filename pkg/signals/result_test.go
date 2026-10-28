package signals

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStatusString(t *testing.T) {
	want := map[Status]string{
		StatusSuccess:             "success",
		StatusNotFound:            "not_found",
		StatusMissingDependencies: "missing_dependencies",
		StatusFailed:              "failed",
		StatusPreconditionFailed:  "precondition_failed",
		Status(9):                 "unknown",
	}
	for s, w := range want {
		if s.String() != w {
			t.Errorf("Status(%d).String() = %q, want %q", int(s), s.String(), w)
		}
	}
	if StatusSuccess != 1 || StatusPreconditionFailed != 5 {
		t.Fatal("status codes must match the Tiga API")
	}
}

func TestResultJSONSourcesNeverNull(t *testing.T) {
	raw, err := json.Marshal(Result{Status: StatusNotFound})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"sources":[]`) || strings.Contains(string(raw), `"error"`) || !strings.Contains(string(raw), `"status":2`) {
		t.Fatalf("json = %s", raw)
	}
	if !(Result{Status: StatusSuccess}).Found() || (Result{Status: StatusNotFound}).Found() {
		t.Fatal("Found mismatch")
	}
}

package salesnavout

import (
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiga-labs/cli-tools/pkg/salesnav"
)

var listSrc = salesnav.Source{Kind: salesnav.KindList, ID: "7", URL: "https://www.linkedin.com/sales/lists/people/7"}

func lead(urn string) salesnav.Lead {
	return salesnav.Lead{FirstName: "F" + urn, LinkedInURN: urn, SourceURL: listSrc.URL}
}

// page returns n leads with URNs prefix0..prefix(n-1).
func page(prefix string, n int) []salesnav.Lead {
	out := make([]salesnav.Lead, n)
	for i := range out {
		out[i] = lead(fmt.Sprintf("%s%d", prefix, i))
	}
	return out
}

func readRows(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func openNew(t *testing.T) (*Writer, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "out.csv")
	w, _, err := Open(Options{Path: path, Source: listSrc, Max: 1000})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w, path
}

func TestDefaultPath(t *testing.T) {
	if got := DefaultPath(listSrc); got != "salesnav-list-7.csv" {
		t.Errorf("DefaultPath = %q", got)
	}
	if got := StatePath("a.csv"); got != "a.csv.state.json" {
		t.Errorf("StatePath = %q", got)
	}
}

func TestOpenNewWritesHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, info, err := Open(Options{Path: path, Source: listSrc, Max: 1000})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if info != (ResumeInfo{Source: listSrc, Max: 1000}) {
		t.Errorf("ResumeInfo = %+v", info)
	}
	rows := readRows(t, path)
	if len(rows) != 1 || strings.Join(rows[0], ",") != strings.Join(salesnav.CSVHeader, ",") {
		t.Fatalf("file = %q, want only the header", rows)
	}
}

func TestOpenExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	os.WriteFile(path, []byte("keep me\n"), 0o644)
	_, _, err := Open(Options{Path: path, Source: listSrc})
	if !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
	if want := path + " already exists. Use --resume to continue it or --force to start over."; err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
	if b, _ := os.ReadFile(path); string(b) != "keep me\n" {
		t.Errorf("existing file changed: %q", b)
	}
}

func TestOpenForce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	os.WriteFile(path, []byte("old\n"), 0o644)
	os.WriteFile(StatePath(path), []byte("{}"), 0o644)
	w, _, err := Open(Options{Path: path, Source: listSrc, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if rows := readRows(t, path); len(rows) != 1 {
		t.Errorf("forced file has %d rows, want header only", len(rows))
	}
	// The old checkpoint is replaced by a fresh one for the new, empty run.
	st := readState(t, path)
	if st["rows"] != 0.0 || st["next_start"] != 0.0 {
		t.Errorf("state after --force = %v, want a fresh empty checkpoint", st)
	}
}

func TestWriteDedupesAndSkipsEmpty(t *testing.T) {
	w, path := openNew(t)
	n, err := w.Write([]salesnav.Lead{lead("A"), lead("B"), lead("A"), lead("")}, 1000)
	if err != nil || n != 2 || w.Rows() != 2 {
		t.Fatalf("first Write = %d, %v; Rows %d", n, err, w.Rows())
	}
	if n, _ := w.Write([]salesnav.Lead{lead("B"), lead("C")}, 1000); n != 1 {
		t.Fatalf("second Write = %d, want 1", n)
	}
	rows := readRows(t, path)
	urnCol := 7
	var got []string
	for _, r := range rows[1:] {
		got = append(got, r[urnCol])
	}
	if strings.Join(got, ",") != "A,B,C" {
		t.Errorf("URNs in file = %v, want A,B,C", got)
	}
}

func TestWriteStopsAtLimit(t *testing.T) {
	w, _ := openNew(t)
	if n, _ := w.Write(page("a", 25), 30); n != 25 {
		t.Fatalf("first page wrote %d", n)
	}
	if n, _ := w.Write(page("b", 25), 30); n != 5 {
		t.Fatalf("second page wrote %d, want 5", n)
	}
	if w.Rows() != 30 {
		t.Fatalf("Rows = %d", w.Rows())
	}
	w2, _ := openNew(t)
	if n, _ := w2.Write(page("c", 25), 10); n != 10 {
		t.Fatalf("limit 10 wrote %d", n)
	}
}

func TestOpenMissingDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nope", "out.csv")
	_, _, err := Open(Options{Path: path, Source: listSrc})
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("err = %v, want an error naming %s", err, path)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("created %v", entries)
	}
}

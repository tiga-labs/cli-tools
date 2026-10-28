package salesnavout

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiga-labs/cli-tools/pkg/salesnav"
)

const searchURL = "https://www.linkedin.com/sales/search/people?savedSearchId=5&sessionId=OLD%3D%3D"

func mustParse(t *testing.T, u string) salesnav.Source {
	t.Helper()
	src, err := salesnav.ParseURL(u)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// stoppedRun writes pages of 25 with a checkpoint after each, then closes like a stopped run.
func stoppedRun(t *testing.T, src salesnav.Source, pages int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "out.csv")
	w, _, err := Open(Options{Path: path, Source: src, Max: 1000})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < pages; i++ {
		w.Write(page(string(rune('a'+i)), 25), 1000)
		if err := w.Checkpoint((i+1)*25, 98); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	return path
}

func TestResumeContinues(t *testing.T) {
	path := stoppedRun(t, listSrc, 2)
	w, info, err := Open(Options{Path: path, Source: listSrc, Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	if !info.Resumed || info.Rows != 50 || info.NextStart != 50 || info.Total != 98 || info.Max != 1000 || info.Source != listSrc {
		t.Fatalf("ResumeInfo = %+v", info)
	}
	next := append([]salesnav.Lead{lead("a0")}, page("z", 24)...)
	if n, err := w.Write(next, 1000); err != nil || n != 24 {
		t.Fatalf("Write after resume = %d, %v; want 24", n, err)
	}
	w.Finish()
	rows := readRows(t, path)
	if len(rows) != 1+74 {
		t.Fatalf("file has %d data rows, want 74", len(rows)-1)
	}
	seen := map[string]bool{}
	for _, r := range rows[1:] {
		if seen[r[7]] {
			t.Fatalf("duplicate URN %s", r[7])
		}
		seen[r[7]] = true
	}
}

func TestResumeTruncatesAfterCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, _, _ := Open(Options{Path: path, Source: listSrc, Max: 1000})
	w.Write(page("a", 25), 1000)
	w.Checkpoint(25, 98)
	checkpointSize := fileSize(t, path)
	w.Write(page("b", 25), 1000) // written, never checkpointed
	w.Close()
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`"Ann,Le`) // half a row from a crash
	f.Close()

	w2, info, err := Open(Options{Path: path, Source: listSrc, Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if info.Rows != 25 || info.NextStart != 25 {
		t.Fatalf("ResumeInfo = %+v, want rows 25 next 25", info)
	}
	if got := fileSize(t, path); got != checkpointSize {
		t.Fatalf("file size %d, want checkpoint size %d", got, checkpointSize)
	}
	// The re-fetched page is written once.
	if n, _ := w2.Write(page("b", 25), 1000); n != 25 {
		t.Fatalf("re-fetched page wrote %d, want 25", n)
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
}

func TestResumeRoundTripsAwkwardValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, _, _ := Open(Options{Path: path, Source: listSrc, Max: 1000})
	awkward := []salesnav.Lead{
		{FirstName: `Ann, "Jr."`, Title: "VP\nSales", LinkedInURN: "u1"},
		{FullName: `"quoted"`, CompanyName: "A,B", LinkedInURN: "u2"},
	}
	w.Write(awkward, 1000)
	w.Checkpoint(2, 2)
	w.Close()
	w2, info, err := Open(Options{Path: path, Source: listSrc, Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()
	if info.Rows != 2 {
		t.Fatalf("Rows = %d, want 2", info.Rows)
	}
	if n, _ := w2.Write(awkward, 1000); n != 0 {
		t.Fatalf("awkward URNs not loaded: wrote %d duplicates", n)
	}
}

func TestResumeNoState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.csv")
	os.WriteFile(path, []byte(strings.Join(salesnav.CSVHeader, ",")+"\n"), 0o644)
	want := "nothing to resume: " + path + ".state.json not found (the earlier run finished, or the file was not written by salesnav-list-import). Use --force to start over."
	if _, _, err := Open(Options{Path: path, Source: listSrc, Resume: true}); err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	missing := filepath.Join(dir, "missing.csv")
	if _, _, err := Open(Options{Path: missing, Source: listSrc, Resume: true}); err == nil || !strings.HasPrefix(err.Error(), "nothing to resume: ") {
		t.Errorf("missing CSV: err = %v", err)
	}
}

func TestResumeMismatch(t *testing.T) {
	path := stoppedRun(t, listSrc, 1)
	other := salesnav.Source{Kind: salesnav.KindList, ID: "8", URL: "https://www.linkedin.com/sales/lists/people/8"}
	want := "--resume URL does not match " + path + ": it was started from " + listSrc.URL
	if _, _, err := Open(Options{Path: path, Source: other, Resume: true}); err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	sameList := mustParse(t, "https://linkedin.com/sales/lists/people/7?sortCriteria=CREATED_TIME")
	w, info, err := Open(Options{Path: path, Source: sameList, Resume: true})
	if err != nil {
		t.Fatalf("same list via another URL: %v", err)
	}
	w.Close()
	if info.Source.URL != listSrc.URL {
		t.Errorf("resumed source URL = %q, want the stored %q", info.Source.URL, listSrc.URL)
	}

	searchPath := stoppedRun(t, mustParse(t, searchURL), 1)
	newSession := mustParse(t, "https://www.linkedin.com/sales/search/people?savedSearchId=5&sessionId=NEW%3D%3D")
	w, info, err = Open(Options{Path: searchPath, Source: newSession, Resume: true})
	if err != nil {
		t.Fatalf("saved search with a new sessionId: %v", err)
	}
	w.Close()
	if info.Source.SessionID != "OLD==" {
		t.Errorf("SessionID = %q, want the stored OLD==", info.Source.SessionID)
	}
}

func TestResumeBadHeader(t *testing.T) {
	path := stoppedRun(t, listSrc, 1)
	b, _ := os.ReadFile(path)
	edited := bytes.Replace(b, []byte("first_name"), []byte("given_name"), 1)
	os.WriteFile(path, edited, 0o644)
	want := path + " does not look like a salesnav-list-import file (unexpected header). Use --force to start over or choose another -o path."
	if _, _, err := Open(Options{Path: path, Source: listSrc, Resume: true}); err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, edited) {
		t.Error("file changed by a failed resume")
	}
}

func TestResumeShrunkFile(t *testing.T) {
	path := stoppedRun(t, listSrc, 2)
	b, _ := os.ReadFile(path)
	short := b[:len(b)-100]
	os.WriteFile(path, short, 0o644)
	want := path + " is shorter than its last checkpoint; it was changed outside salesnav-list-import. Use --force to start over."
	if _, _, err := Open(Options{Path: path, Source: listSrc, Resume: true}); err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, short) {
		t.Error("file changed by a failed resume")
	}
}

func TestResumeMax(t *testing.T) {
	path := stoppedRun(t, listSrc, 1)
	w, info, err := Open(Options{Path: path, Source: listSrc, Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	if info.Max != 1000 {
		t.Errorf("Max with caller 0 = %d, want stored 1000", info.Max)
	}
	w, info, _ = Open(Options{Path: path, Source: listSrc, Resume: true, Max: 500})
	w.Close()
	if info.Max != 500 {
		t.Errorf("Max with caller 500 = %d", info.Max)
	}
}

// An edit before the checkpoint that keeps the file long enough must be refused, not
// truncated mid-row (S4 review C1).
func TestResumeEditedValueRejected(t *testing.T) {
	path := stoppedRun(t, listSrc, 1)
	b, _ := os.ReadFile(path)
	edited := bytes.Replace(b, []byte("Fa0,"), []byte("Fa0-a-much-longer-name,"), 1)
	if bytes.Equal(edited, b) {
		t.Fatal("test setup: nothing replaced")
	}
	os.WriteFile(path, edited, 0o644)
	want := path + " was changed outside salesnav-list-import since its last checkpoint. Use --force to start over."
	if _, _, err := Open(Options{Path: path, Source: listSrc, Resume: true}); err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, edited) {
		t.Error("file changed by a failed resume")
	}
}

// Ctrl-C before the first page is written must still leave a resumable file (S4 review I1).
func TestStoppedBeforeFirstPageIsResumable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.csv")
	w, _, err := Open(Options{Path: path, Source: listSrc, Max: 300})
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	w2, info, err := Open(Options{Path: path, Source: listSrc, Resume: true})
	if err != nil {
		t.Fatalf("resume before first page: %v", err)
	}
	defer w2.Close()
	if !info.Resumed || info.Rows != 0 || info.NextStart != 0 || info.Max != 300 {
		t.Errorf("ResumeInfo = %+v", info)
	}
}

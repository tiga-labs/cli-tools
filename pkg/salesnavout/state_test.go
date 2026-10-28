package salesnavout

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func readState(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(StatePath(path))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCheckpointWritesState(t *testing.T) {
	w, path := openNew(t)
	w.Write(page("a", 25), 1000)
	if err := w.Checkpoint(25, 98); err != nil {
		t.Fatal(err)
	}
	st := readState(t, path)
	info, _ := os.Stat(path)
	want := map[string]any{"version": 1.0, "url": listSrc.URL, "next_start": 25.0, "rows": 25.0, "max": 1000.0, "total": 98.0, "bytes": float64(info.Size())}
	for k, v := range want {
		if st[k] != v {
			t.Errorf("state[%s] = %v, want %v", k, st[k], v)
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 2 {
		t.Errorf("dir has %d entries, want the CSV and the state file only", len(entries))
	}
}

func TestFinishRemovesState(t *testing.T) {
	w, path := openNew(t)
	w.Write(page("a", 3), 1000)
	w.Checkpoint(3, 3)
	if err := w.Finish(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(StatePath(path)); !os.IsNotExist(err) {
		t.Errorf("state file kept after Finish: %v", err)
	}
	if rows := readRows(t, path); len(rows) != 4 {
		t.Errorf("CSV has %d rows, want header + 3", len(rows))
	}
}

func TestCloseKeepsState(t *testing.T) {
	w, path := openNew(t)
	w.Write(page("a", 3), 1000)
	w.Checkpoint(3, 10)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(StatePath(path)); err != nil {
		t.Errorf("state file gone after Close: %v", err)
	}
}

func TestCloseTwice(t *testing.T) {
	w, _ := openNew(t)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Finish(); err != nil {
		t.Errorf("Finish after Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// After a failed Write, nothing may be checkpointed (S4 review I2).
func TestWriteErrorIsSticky(t *testing.T) {
	w, path := openNew(t)
	w.Write(page("a", 2), 1000)
	if err := w.Checkpoint(2, 10); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(StatePath(path))
	w.csv = csv.NewWriter(failWriter{}) // the disk fails under the writer; the file itself stays open
	_, werr := w.Write(page("b", 2), 1000)
	if werr == nil {
		t.Fatal("Write on a failed file: want error")
	}
	if err := w.Checkpoint(4, 10); err == nil {
		t.Fatal("Checkpoint after a failed Write: want error")
	}
	if _, err := w.Write(page("c", 2), 1000); err == nil {
		t.Fatal("later Write: want the stored error")
	}
	if after, _ := os.ReadFile(StatePath(path)); !bytes.Equal(after, before) {
		t.Error("checkpoint changed after a failed Write")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

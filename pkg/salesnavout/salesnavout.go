// Package salesnavout writes Sales Navigator leads to the output CSV page by page and keeps
// a checkpoint beside it, so a stopped import keeps what it pulled and can be resumed.
package salesnavout

import (
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"slices"

	"github.com/tiga-labs/cli-tools/pkg/salesnav"
)

// ErrExists is returned when the output file exists and neither --resume nor --force is set.
var ErrExists = errors.New("already exists. Use --resume to continue it or --force to start over.")

// DefaultPath is the output file name used when -o is not given.
func DefaultPath(src salesnav.Source) string {
	return "salesnav-" + src.Slug() + ".csv"
}

// StatePath is the checkpoint file kept beside the output while an import is unfinished.
func StatePath(out string) string {
	return out + ".state.json"
}

// Options says which file to write and how to treat an existing one.
type Options struct {
	Path   string
	Source salesnav.Source
	// Max is the --max the caller wants; on resume, 0 means "use the stored one".
	Max           int
	Resume, Force bool
}

// ResumeInfo describes where the import starts. For a new file only Source and Max are set.
type ResumeInfo struct {
	Resumed bool
	// Source is what to fetch; on resume it is rebuilt from the stored URL.
	Source                      salesnav.Source
	Rows, NextStart, Total, Max int
}

// Writer appends leads to the output CSV.
type Writer struct {
	path string
	src  salesnav.Source
	max  int
	file *os.File
	csv  *csv.Writer
	// hash covers every byte written, so a checkpoint can tell if the file was edited later.
	hash   hash.Hash
	seen   map[string]bool
	rows   int
	closed bool
	// err is the first write failure; after it nothing more is written or checkpointed.
	err error
}

func newWriter(path string, src salesnav.Source, max int, f *os.File, h hash.Hash, seen map[string]bool, rows int) *Writer {
	return &Writer{path: path, src: src, max: max, file: f, csv: csv.NewWriter(io.MultiWriter(f, h)), hash: h, seen: seen, rows: rows}
}

// Open creates (or with Force, replaces) the output file, or reopens it with Resume.
func Open(opts Options) (*Writer, ResumeInfo, error) {
	if opts.Resume {
		return resume(opts)
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if opts.Force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		if err := os.Remove(StatePath(opts.Path)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, ResumeInfo{}, err
		}
	}
	f, err := os.OpenFile(opts.Path, flags, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil, ResumeInfo{}, fmt.Errorf("%s %w", opts.Path, ErrExists)
	}
	if err != nil {
		return nil, ResumeInfo{}, err
	}
	w := newWriter(opts.Path, opts.Source, opts.Max, f, sha256.New(), map[string]bool{}, 0)
	// Checkpoint before the first page so a run stopped during it can still be resumed.
	if err := w.flushRows([][]string{salesnav.CSVHeader}); err != nil {
		f.Close()
		return nil, ResumeInfo{}, err
	}
	if err := w.Checkpoint(0, 0); err != nil {
		f.Close()
		return nil, ResumeInfo{}, err
	}
	return w, ResumeInfo{Source: opts.Source, Max: opts.Max}, nil
}

// Write appends the leads not already in the file, skipping leads without a URN, until the
// file holds limit rows. The page is on disk when Write returns. It returns how many rows
// this call wrote.
func (w *Writer) Write(leads []salesnav.Lead, limit int) (int, error) {
	var out [][]string
	var urns []string
	page := map[string]bool{}
	for _, l := range leads {
		if w.rows+len(out) >= limit {
			break
		}
		if l.LinkedInURN == "" || w.seen[l.LinkedInURN] || page[l.LinkedInURN] {
			continue
		}
		page[l.LinkedInURN] = true
		urns = append(urns, l.LinkedInURN)
		out = append(out, l.Row())
	}
	if err := w.flushRows(out); err != nil {
		return 0, err
	}
	for _, u := range urns {
		w.seen[u] = true
	}
	w.rows += len(out)
	return len(out), nil
}

// Rows is the number of leads in the file.
func (w *Writer) Rows() int { return w.rows }

func (w *Writer) flushRows(rows [][]string) error {
	if w.err != nil {
		return w.err
	}
	if err := w.csv.WriteAll(rows); err != nil {
		w.err = fmt.Errorf("write %s: %w", w.path, err)
		return w.err
	}
	if err := w.file.Sync(); err != nil {
		w.err = fmt.Errorf("write %s: %w", w.path, err)
		return w.err
	}
	return nil
}

// Close closes the file and keeps the checkpoint, for a stopped import.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.file.Close()
}

// resume reopens a stopped import. Every check runs before the file is touched, so a
// failed resume leaves it as it was.
func resume(opts Options) (*Writer, ResumeInfo, error) {
	b, err := os.ReadFile(StatePath(opts.Path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ResumeInfo{}, fmt.Errorf("nothing to resume: %s not found (the earlier run finished, or the file was not written by salesnav-list-import). Use --force to start over.", StatePath(opts.Path))
	}
	if err != nil {
		return nil, ResumeInfo{}, err
	}
	var st state
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, ResumeInfo{}, fmt.Errorf("read %s: %w", StatePath(opts.Path), err)
	}
	// Rebuild the source only from the stored URL, so nothing unchecked reaches a request.
	src, err := salesnav.ParseURL(st.URL)
	if err != nil {
		return nil, ResumeInfo{}, fmt.Errorf("read %s: %w", StatePath(opts.Path), err)
	}
	// Same list or search, even if the URL was copied again (a saved search gets a new sessionId).
	if src.Kind != opts.Source.Kind || src.ID != opts.Source.ID {
		return nil, ResumeInfo{}, fmt.Errorf("--resume URL does not match %s: it was started from %s", opts.Path, st.URL)
	}

	f, err := os.OpenFile(opts.Path, os.O_RDWR, 0)
	if err != nil {
		return nil, ResumeInfo{}, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, ResumeInfo{}, err
	}
	if info.Size() < st.Bytes {
		f.Close()
		return nil, ResumeInfo{}, fmt.Errorf("%s is shorter than its last checkpoint; it was changed outside salesnav-list-import. Use --force to start over.", opts.Path)
	}
	// Read only up to the checkpoint (anything after it is re-fetched), hashing as we go.
	h := sha256.New()
	prefix := io.TeeReader(io.LimitReader(f, st.Bytes), h)
	r := csv.NewReader(prefix)
	r.FieldsPerRecord = len(salesnav.CSVHeader)
	header, err := r.Read()
	if err != nil || !slices.Equal(header, salesnav.CSVHeader) {
		f.Close()
		return nil, ResumeInfo{}, fmt.Errorf("%s does not look like a salesnav-list-import file (unexpected header). Use --force to start over or choose another -o path.", opts.Path)
	}
	urnCol := slices.Index(salesnav.CSVHeader, "linkedin_urn")
	seen := map[string]bool{}
	rows := 0
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			f.Close()
			return nil, ResumeInfo{}, fmt.Errorf("read %s: %w", opts.Path, err)
		}
		seen[rec[urnCol]] = true
		rows++
	}
	if _, err := io.Copy(io.Discard, prefix); err != nil {
		f.Close()
		return nil, ResumeInfo{}, err
	}
	if hex.EncodeToString(h.Sum(nil)) != st.SHA256 || rows != st.Rows {
		f.Close()
		return nil, ResumeInfo{}, fmt.Errorf("%s was changed outside salesnav-list-import since its last checkpoint. Use --force to start over.", opts.Path)
	}

	if err := f.Truncate(st.Bytes); err != nil {
		f.Close()
		return nil, ResumeInfo{}, err
	}
	if _, err := f.Seek(st.Bytes, io.SeekStart); err != nil {
		f.Close()
		return nil, ResumeInfo{}, err
	}
	maxRows := opts.Max
	if maxRows == 0 {
		maxRows = st.Max
	}
	w := newWriter(opts.Path, src, maxRows, f, h, seen, rows)
	return w, ResumeInfo{Resumed: true, Source: src, Rows: rows, NextStart: st.NextStart, Total: st.Total, Max: maxRows}, nil
}

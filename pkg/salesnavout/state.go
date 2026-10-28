package salesnavout

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const stateVersion = 1

// state is the checkpoint beside the output CSV. Bytes is the CSV's size at the
// checkpoint and SHA256 the hash of those bytes; resume refuses a file whose first Bytes
// bytes changed, then truncates the CSV back to Bytes.
type state struct {
	Version   int    `json:"version"`
	URL       string `json:"url"`
	NextStart int    `json:"next_start"`
	Rows      int    `json:"rows"`
	Max       int    `json:"max"`
	Total     int    `json:"total"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256"`
}

// Checkpoint records that every page before nextStart is on disk. Call it after Write.
func (w *Writer) Checkpoint(nextStart, total int) error {
	if w.err != nil {
		return w.err
	}
	info, err := w.file.Stat()
	if err != nil {
		return fmt.Errorf("checkpoint %s: %w", w.path, err)
	}
	st := state{Version: stateVersion, URL: w.src.URL, NextStart: nextStart, Rows: w.rows, Max: w.max, Total: total, Bytes: info.Size(), SHA256: hex.EncodeToString(w.hash.Sum(nil))}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(StatePath(w.path), b)
}

// Finish closes the file and removes the checkpoint, for an import that completed.
func (w *Writer) Finish() error {
	if err := w.Close(); err != nil {
		return err
	}
	if err := os.Remove(StatePath(w.path)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// writeAtomic replaces path with data via a temp file in the same directory.
func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("checkpoint %s: %w", path, err)
	}
	defer os.Remove(tmp.Name()) // no-op after the rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("checkpoint %s: %w", path, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("checkpoint %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("checkpoint %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("checkpoint %s: %w", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("checkpoint %s: %w", path, err)
	}
	return nil
}

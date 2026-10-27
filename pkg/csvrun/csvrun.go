// Package csvrun runs a per-row function over a CSV concurrently and writes the original
// columns plus appended columns in input order.
package csvrun

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// RowFunc processes one data row and returns the cells to append to it. It must check
// ctx.Err() itself and, when cancelled, return an error row without calling vendors.
type RowFunc func(ctx context.Context, index int, row []string) []string

// Options configures Process.
type Options struct {
	// Concurrency is the worker count; <= 0 means 5.
	Concurrency int
	// AppendedHeaders are added after the input headers.
	AppendedHeaders []string
	// OnRowDone, if set, is called once per finished row, always from a single goroutine.
	OnRowDone func(completed, total, index int)
}

// Process reads all rows from r, builds a RowFunc from the header via prepare, runs it across
// a worker pool, and writes the original columns plus the appended cells to w in the exact
// original row order. Rows are flushed as soon as every earlier row has finished, so completed
// (paid) results reach the output even if the run is interrupted.
func Process(ctx context.Context, r io.Reader, w io.Writer, opts Options, prepare func(headers []string) (RowFunc, error)) error {
	if ctx == nil {
		return errors.New("ctx is nil")
	}
	if r == nil || w == nil {
		return errors.New("reader and writer are required")
	}

	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1 // allow variable trailing fields
	reader.LazyQuotes = true

	allRows, err := reader.ReadAll()
	if err != nil {
		return fmt.Errorf("failed to read CSV: %w", err)
	}
	if len(allRows) == 0 {
		return errors.New("CSV input is empty")
	}

	headers := allRows[0]
	dataRows := allRows[1:]

	rowFn, err := prepare(headers)
	if err != nil {
		return err
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 5
	}

	total := len(dataRows)
	appended := make([][]string, total)

	writer := csv.NewWriter(w)
	outHeader := make([]string, 0, len(headers)+len(opts.AppendedHeaders))
	outHeader = append(outHeader, headers...)
	outHeader = append(outHeader, opts.AppendedHeaders...)
	if err := writer.Write(outHeader); err != nil {
		return err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}

	jobs := make(chan int, total)
	for i := range dataRows {
		jobs <- i
	}
	close(jobs)

	// done receives the index of each finished row; buffered so workers never block on the writer.
	done := make(chan int, total)

	numWorkers := min(concurrency, total)
	if numWorkers == 0 {
		numWorkers = 1
	}

	var wg sync.WaitGroup
	for range numWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				appended[idx] = rowFn(ctx, idx, dataRows[idx])
				done <- idx
			}
		}()
	}
	go func() {
		wg.Wait()
		close(done)
	}()

	finished := make([]bool, total)
	next := 0
	completedCount := 0
	var writeErr error
	for idx := range done {
		finished[idx] = true
		completedCount++
		if opts.OnRowDone != nil {
			opts.OnRowDone(completedCount, total, idx)
		}
		if writeErr != nil {
			continue
		}
		for next < total && finished[next] {
			if err := writer.Write(outputRow(len(headers), dataRows[next], appended[next])); err != nil {
				writeErr = err
				break
			}
			next++
		}
		writer.Flush()
		if err := writer.Error(); err != nil && writeErr == nil {
			writeErr = err
		}
	}
	if writeErr != nil {
		return writeErr
	}
	return ctx.Err()
}

// outputRow pads origRow to the header width and appends the computed cells.
func outputRow(width int, origRow, appended []string) []string {
	padded := make([]string, width, width+len(appended))
	copy(padded, origRow)
	return append(padded, appended...)
}

// NormalizeHeader lowercases and trims a header, strips a UTF-8 BOM, and turns '-' and ' ' into '_'.
func NormalizeHeader(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimPrefix(h, "\ufeff") // strip UTF-8 BOM if present
	h = strings.ReplaceAll(h, "-", "_")
	h = strings.ReplaceAll(h, " ", "_")
	return h
}

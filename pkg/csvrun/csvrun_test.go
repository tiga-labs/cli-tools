package csvrun

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func readCSV(t *testing.T, s string) [][]string {
	t.Helper()
	rows, err := csv.NewReader(strings.NewReader(s)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func constPrepare(fn RowFunc) func([]string) (RowFunc, error) {
	return func([]string) (RowFunc, error) { return fn, nil }
}

func TestProcessPreservesOrder(t *testing.T) {
	var in strings.Builder
	in.WriteString("name\n")
	for i := 0; i < 20; i++ {
		in.WriteString("r" + strconv.Itoa(i) + "\n")
	}
	var out bytes.Buffer
	err := Process(context.Background(), strings.NewReader(in.String()), &out,
		Options{Concurrency: 8, AppendedHeaders: []string{"idx"}},
		constPrepare(func(_ context.Context, index int, _ []string) []string {
			time.Sleep(time.Duration(20-index) * time.Millisecond)
			return []string{strconv.Itoa(index)}
		}))
	if err != nil {
		t.Fatal(err)
	}
	rows := readCSV(t, out.String())
	if strings.Join(rows[0], ",") != "name,idx" {
		t.Fatalf("header = %v", rows[0])
	}
	for i, row := range rows[1:] {
		if row[0] != "r"+strconv.Itoa(i) || row[1] != strconv.Itoa(i) {
			t.Fatalf("row %d = %v", i, row)
		}
	}
}

func TestProcessPadsShortRows(t *testing.T) {
	var out bytes.Buffer
	err := Process(context.Background(), strings.NewReader("a,b,c\n1\n"), &out,
		Options{AppendedHeaders: []string{"x"}},
		constPrepare(func(context.Context, int, []string) []string { return []string{"X"} }))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(strings.Split(out.String(), "\n")[1]); got != "1,,,X" {
		t.Fatalf("row = %q", got)
	}
}

func TestProcessOnRowDoneSerialized(t *testing.T) {
	var in strings.Builder
	in.WriteString("n\n")
	for i := 0; i < 50; i++ {
		in.WriteString("x\n")
	}
	count := 0 // deliberately unsynchronized: -race fails if OnRowDone runs concurrently
	err := Process(context.Background(), strings.NewReader(in.String()), io.Discard,
		Options{Concurrency: 10, OnRowDone: func(completed, total, index int) { count++ }},
		constPrepare(func(context.Context, int, []string) []string { return nil }))
	if err != nil || count != 50 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestProcessCancelledReturnsCtxErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err := Process(ctx, strings.NewReader("n\na\nb\n"), &out, Options{AppendedHeaders: []string{"status"}},
		constPrepare(func(ctx context.Context, _ int, _ []string) []string {
			if ctx.Err() != nil {
				return []string{"error"}
			}
			return []string{"ok"}
		}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if rows := readCSV(t, out.String()); len(rows) != 3 || rows[1][1] != "error" {
		t.Fatalf("rows = %v", rows)
	}
}

func TestProcessEmptyInput(t *testing.T) {
	err := Process(context.Background(), strings.NewReader(""), io.Discard, Options{},
		constPrepare(func(context.Context, int, []string) []string { return nil }))
	if err == nil || !strings.Contains(err.Error(), "CSV input is empty") {
		t.Fatalf("err = %v", err)
	}
}

func TestProcessPrepareErrorStops(t *testing.T) {
	err := Process(context.Background(), strings.NewReader("a\n1\n"), io.Discard, Options{},
		func([]string) (RowFunc, error) { return nil, errors.New("bad column") })
	if err == nil || err.Error() != "bad column" {
		t.Fatalf("err = %v", err)
	}
}

func TestNormalizeHeader(t *testing.T) {
	if got := NormalizeHeader("\ufeffFirst Name"); got != "first_name" {
		t.Fatalf("got %q", got)
	}
	if got := NormalizeHeader(" Company-LinkedIn "); got != "company_linkedin" {
		t.Fatalf("got %q", got)
	}
}

func TestWriteOutputRefusesSameFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "in.csv")
	_ = os.WriteFile(path, []byte("a\n"), 0644)
	err := WriteOutput(path, path, io.Discard, ".t-*.csv", func(io.Writer) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "is the same file as the input") {
		t.Fatalf("err = %v", err)
	}
}

func TestWriteOutputToStdout(t *testing.T) {
	var stdout bytes.Buffer
	err := WriteOutput("in.csv", "-", &stdout, ".t-*.csv", func(w io.Writer) error {
		_, err := io.WriteString(w, "hi")
		return err
	})
	if err != nil || stdout.String() != "hi" {
		t.Fatalf("stdout=%q err=%v", stdout.String(), err)
	}
}

func TestWriteOutputKeepsPartialOnCancel(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.csv")
	err := WriteOutput("", out, io.Discard, ".t-*.csv", func(w io.Writer) error {
		_, _ = io.WriteString(w, "partial")
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	data, rerr := os.ReadFile(out)
	if rerr != nil || string(data) != "partial" {
		t.Fatalf("out=%q err=%v", data, rerr)
	}
	if info, _ := os.Stat(out); info.Mode().Perm() != 0644 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestWriteOutputDiscardsOnError(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.csv")
	err := WriteOutput("", out, io.Discard, ".t-*.csv", func(w io.Writer) error {
		_, _ = io.WriteString(w, "junk")
		return errors.New("boom")
	})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("err = %v", err)
	}
	if _, serr := os.Stat(out); !os.IsNotExist(serr) {
		t.Fatalf("target should not exist: %v", serr)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".t-*")); len(left) > 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

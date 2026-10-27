package csvrun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// WriteOutput runs run against stdout when outPath is empty or "-", otherwise against a 0644
// temp file beside outPath that is renamed into place on success or context.Canceled (keeping
// partial results) and discarded on any other error, so an existing output file is never
// truncated by a run that fails early. It refuses to write over inPath.
func WriteOutput(inPath, outPath string, stdout io.Writer, tmpPattern string, run func(w io.Writer) error) error {
	if outPath == "" || outPath == "-" {
		return run(stdout)
	}
	if inPath != "" && inPath != "-" {
		same, err := IsSameFile(inPath, outPath)
		if err != nil {
			return err
		}
		if same {
			return fmt.Errorf("output CSV %q is the same file as the input; choose a different -o path", outPath)
		}
	}

	tmpOut, err := os.CreateTemp(filepath.Dir(outPath), tmpPattern)
	if err != nil {
		return fmt.Errorf("failed to create output CSV %q: %w", outPath, err)
	}
	defer os.Remove(tmpOut.Name()) // no-op after a successful rename
	defer tmpOut.Close()
	if err := tmpOut.Chmod(0644); err != nil {
		return fmt.Errorf("failed to create output CSV %q: %w", outPath, err)
	}

	runErr := run(tmpOut)
	// Keep partial results on cancellation; discard the temp file on any other failure.
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}
	if err := tmpOut.Close(); err != nil {
		return fmt.Errorf("failed to write output CSV %q: %w", outPath, err)
	}
	if err := os.Rename(tmpOut.Name(), outPath); err != nil {
		return fmt.Errorf("failed to write output CSV %q: %w", outPath, err)
	}
	return runErr
}

// IsSameFile reports whether a and b refer to the same file on disk.
func IsSameFile(a, b string) (bool, error) {
	absA, err := filepath.Abs(a)
	if err != nil {
		return false, err
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return false, err
	}
	if absA == absB {
		return true, nil
	}
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return false, nil
	}
	return os.SameFile(infoA, infoB), nil
}

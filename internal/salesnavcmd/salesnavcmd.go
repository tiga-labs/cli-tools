// Package salesnavcmd implements `tiga salesnav-list-import`: import a Sales Navigator lead
// list or saved search from the user's own logged-in Chrome into a CSV.
package salesnavcmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"github.com/tiga-labs/cli-tools/pkg/chromeattach"
	"github.com/tiga-labs/cli-tools/pkg/cliflags"
	"github.com/tiga-labs/cli-tools/pkg/salesnav"
	"github.com/tiga-labs/cli-tools/pkg/salesnavout"
)

const usageLine = "usage: tiga salesnav-list-import <lead-list-or-saved-search-url> [-o out.csv] [--max N] [--resume | --force]"

// Run implements `tiga salesnav-list-import`.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("tiga salesnav-list-import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("o", "", "output CSV (default salesnav-<list|search>-<id>.csv)")
	maxLeads := fs.Int("max", 0, fmt.Sprintf("most leads to import, 1-%d (default %d; on --resume, the value the import started with)", salesnav.MaxLimit, salesnav.DefaultMax))
	resume := fs.Bool("resume", false, "continue a stopped import in the -o file")
	force := fs.Bool("force", false, "replace an existing -o file and start over")
	chromeDir := fs.String("chrome-user-data-dir", "", "Chrome user data directory (default: Google Chrome's)")
	fs.Usage = func() {
		fmt.Fprintf(stderr, `Usage:
  tiga salesnav-list-import <lead-list-or-saved-search-url> [-o out.csv] [--max N] [--resume | --force]

Imports from the Chrome you are logged into Sales Nav with. Turn on remote debugging once at
chrome://inspect/#remote-debugging (Chrome 144+) and click Allow when Chrome asks. Paging is
slow on purpose and stops at LinkedIn's first warning (exit code 2).

Flags:
`)
		fs.PrintDefaults()
	}

	positional, err := parseFlags(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New(usageLine)
	}
	if *resume && *force {
		return errors.New("--resume and --force cannot be used together")
	}
	src, err := salesnav.ParseURL(positional[0])
	if err != nil {
		return err
	}
	maxSet := false
	fs.Visit(func(f *flag.Flag) { maxSet = maxSet || f.Name == "max" })
	switch {
	case maxSet:
		if err := salesnav.ValidateMax(*maxLeads); err != nil {
			return err
		}
	case !*resume:
		*maxLeads = salesnav.DefaultMax
	}
	path := *out
	if path == "" {
		path = salesnavout.DefaultPath(src)
	}

	w, info, err := salesnavout.Open(salesnavout.Options{Path: path, Source: src, Max: *maxLeads, Resume: *resume, Force: *force})
	if err != nil {
		return err
	}
	j := job{src: info.Source, out: path, max: info.Max, w: w, info: info, chromeDir: *chromeDir}
	d := deps{
		connect: func(ctx context.Context) (browser, error) {
			b, err := chromeattach.Connect(ctx, chromeattach.Options{UserDataDir: *chromeDir, Stderr: stderr})
			if err != nil {
				return nil, err
			}
			return chromeBrowser{b}, nil
		},
		sleep: sleepCtx,
		rand:  globalRand{},
		profileName: func() string {
			dir := *chromeDir
			if dir == "" {
				dir, _ = chromeattach.DefaultUserDataDir()
			}
			return profileName(dir)
		},
	}
	return runImport(ctx, j, d, stdout, stderr)
}

// parseFlags lets flags come before or after the URL.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	value := map[string]bool{}
	fs.VisitAll(func(fl *flag.Flag) {
		if b, ok := fl.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			return
		}
		value["-"+fl.Name], value["--"+fl.Name] = true, true
	})
	positional, flagArgs := cliflags.SplitPositionalAndFlags(args, value)
	return positional, fs.Parse(flagArgs)
}

// profileName is the display name of Chrome's first ("Default") profile, the one tiga
// attaches to, read from Chrome's Local State file.
func profileName(userDataDir string) string {
	b, err := os.ReadFile(filepath.Join(userDataDir, "Local State"))
	if err != nil {
		return "Default"
	}
	var ls struct {
		Profile struct {
			InfoCache map[string]struct {
				Name string `json:"name"`
			} `json:"info_cache"`
		} `json:"profile"`
	}
	if json.Unmarshal(b, &ls) != nil || ls.Profile.InfoCache["Default"].Name == "" {
		return "Default"
	}
	return ls.Profile.InfoCache["Default"].Name
}

type chromeBrowser struct{ *chromeattach.Browser }

func (b chromeBrowser) OpenTab(ctx context.Context, url string) (tab, error) {
	t, err := b.Browser.OpenTab(ctx, url)
	if err != nil {
		return nil, err // never a typed-nil tab
	}
	return t, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type globalRand struct{}

func (globalRand) Int64N(n int64) int64 { return rand.Int64N(n) }

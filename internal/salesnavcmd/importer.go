package salesnavcmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tiga-labs/cli-tools/pkg/salesnav"
	"github.com/tiga-labs/cli-tools/pkg/salesnavout"
)

const (
	salesHome    = "https://www.linkedin.com/sales/home"
	loadTimeout  = 60 * time.Second
	fetchTimeout = 60 * time.Second
)

type tab interface {
	WaitLoaded(ctx context.Context) error
	Eval(ctx context.Context, expr string, out any) error
	Close() error
}

type browser interface {
	OpenTab(ctx context.Context, url string) (tab, error)
	Close() error
}

// deps is everything runImport needs from the outside world, so tests can fake it.
type deps struct {
	connect     func(ctx context.Context) (browser, error)
	sleep       func(ctx context.Context, d time.Duration) error
	rand        salesnav.Rand
	profileName func() string
}

// job is one import: what to fetch and the opened output file.
type job struct {
	src  salesnav.Source
	out  string
	max  int
	w    *salesnavout.Writer
	info salesnavout.ResumeInfo
	// chromeDir is --chrome-user-data-dir, repeated in the resume hint when set.
	chromeDir string
}

// exitError carries the message and exit code for a run that ends in a known way.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }
func (e *exitError) ExitCode() int { return e.code }

// runImport pages through the source at the Pacer's pace, writing and checkpointing each
// page, and stops on the first warning. It always closes the writer, tab and browser.
func runImport(ctx context.Context, j job, d deps, stdout, stderr io.Writer) error {
	pacer := salesnav.Pacer{R: d.rand}
	if j.w.Rows() >= j.max {
		return j.done(stdout, salesnav.DoneMax, 0)
	}

	b, err := d.connect(ctx)
	if err != nil {
		j.abandon()
		return err
	}
	defer b.Close()
	lctx, cancel := context.WithTimeout(ctx, loadTimeout)
	t, err := b.OpenTab(lctx, salesHome)
	if err == nil {
		defer t.Close()
		err = t.WaitLoaded(lctx)
	}
	cancel()
	if err != nil {
		j.abandon()
		return &exitError{1, fmt.Sprintf("could not load Sales Navigator in Chrome: %v", err)}
	}

	requested, prev := j.info.NextStart, j.w.Rows()
	wait := pacer.FirstWait()
	for page := 1; ; page++ {
		if err := d.sleep(ctx, wait); err != nil {
			return j.interrupted(stderr, err)
		}
		// A timer and Ctrl-C can fire together; never send a request after Ctrl-C.
		if ctx.Err() != nil {
			return j.interrupted(stderr, ctx.Err())
		}
		var res salesnav.FetchResult
		fctx, cancel := context.WithTimeout(ctx, fetchTimeout)
		err := t.Eval(fctx, salesnav.FetchJS(j.src.PageURL(requested)), &res)
		cancel()
		if ctx.Err() != nil {
			return j.interrupted(stderr, ctx.Err())
		}
		if err != nil {
			return j.stop(fmt.Sprintf("the fetch in Chrome failed: %v", err))
		}

		p, err := salesnav.CheckPage(res, j.src)
		var stop *salesnav.Stop
		if errors.As(err, &stop) {
			if stop.Kind == salesnav.StopNotLoggedIn && page == 1 && !j.info.Resumed && j.w.Rows() == 0 {
				j.abandon()
				return &exitError{1, fmt.Sprintf("Sales Nav is not logged in in your Chrome profile %q. tiga can only use Chrome's first profile; log into Sales Nav there and run this again.", d.profileName())}
			}
			return j.stop(stop.Reason)
		}
		if err != nil {
			return j.stop(err.Error())
		}
		if page == 1 && j.info.Resumed && j.info.Total > 0 && p.Total != j.info.Total {
			fmt.Fprintf(stderr, "Note: LinkedIn now reports %d leads (%d when this import started); leads added or removed since then shift the pages, so some may be missed.\n", p.Total, j.info.Total)
		}

		n, err := j.w.Write(p.Leads, j.max)
		if err != nil {
			return j.failed(err)
		}
		rows := j.w.Rows()
		next, done, err := salesnav.NextStart(requested, p, rows, j.max)
		if err != nil {
			return j.stop(err.Error())
		}
		if done != salesnav.NotDone {
			return j.done(stdout, done, p.Total)
		}
		if err := j.w.Checkpoint(next, p.Total); err != nil {
			return j.failed(err)
		}
		wait = pacer.NextWait(prev, rows)
		fmt.Fprintf(stderr, "page %d: %d new leads, %d total (max %d); next page in %ds\n", page, n, rows, j.max, int(wait.Round(time.Second)/time.Second))
		prev, requested = rows, next
	}
}

// done finishes the file and prints the summary and the next step.
func (j job) done(stdout io.Writer, why salesnav.DoneReason, total int) error {
	if err := j.w.Finish(); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Done: %d leads in %s.\n", j.w.Rows(), j.out)
	switch {
	case why == salesnav.DoneMax:
		fmt.Fprintf(stdout, "Stopped at --max %d.\n", j.max)
	case why == salesnav.DoneEndedEarly && j.src.Kind == salesnav.KindSavedSearch:
		fmt.Fprintf(stdout, "LinkedIn stopped returning results before its total of %d. Sales Nav limits saved-search results; use a lead list to import more.\n", total)
	case why == salesnav.DoneEndedEarly:
		fmt.Fprintf(stdout, "LinkedIn stopped returning leads before its total of %d; the list may have changed during the import.\n", total)
	}
	enriched := strings.TrimSuffix(j.out, filepath.Ext(j.out)) + "-enriched.csv"
	fmt.Fprintf(stdout, "Next: tiga enrich csv -i %s -o %s\n", shellQuote(j.out), shellQuote(enriched))
	return nil
}

// stop ends a run LinkedIn (or the fetch) pushed back on: exit 2, file kept for --resume.
func (j job) stop(reason string) error {
	if j.abandon() {
		return &exitError{2, fmt.Sprintf("Stopped: %s. No leads were saved.\nWait before trying again (hours, not minutes), then run the same command again.", reason)}
	}
	j.w.Close()
	return &exitError{2, fmt.Sprintf("Stopped: %s. %d leads saved in %s.\nWait before trying again (hours, not minutes). Resume with: %s", reason, j.w.Rows(), j.out, j.resumeCommand())}
}

// interrupted handles Ctrl-C: the file is kept for --resume unless nothing was saved.
func (j job) interrupted(stderr io.Writer, err error) error {
	if j.abandon() {
		fmt.Fprintln(stderr, "Interrupted. No leads were saved.")
		return err
	}
	j.w.Close()
	fmt.Fprintf(stderr, "Interrupted. %d leads saved in %s. Resume with: %s\n", j.w.Rows(), j.out, j.resumeCommand())
	return err
}

// failed ends a run on a local error (disk full, file problem): exit 1, with how to continue.
func (j job) failed(err error) error {
	if j.abandon() {
		return &exitError{1, fmt.Sprintf("%v. No leads were saved.", err)}
	}
	j.w.Close()
	return &exitError{1, fmt.Sprintf("%v. %d leads saved in %s.\nFix the problem (for example free up disk space), then resume with: %s", err, j.w.Rows(), j.out, j.resumeCommand())}
}

// resumeCommand is a shell-safe command that continues this import. The URL is rebuilt from
// the validated source, so nothing else pasted into the original URL is repeated.
func (j job) resumeCommand() string {
	cmd := fmt.Sprintf("tiga salesnav-list-import %s -o %s --resume", singleQuote(canonicalURL(j.src)), shellQuote(j.out))
	if j.chromeDir != "" {
		cmd += " --chrome-user-data-dir " + shellQuote(j.chromeDir)
	}
	return cmd
}

// canonicalURL is the Sales Nav URL for src built only from its validated fields.
func canonicalURL(src salesnav.Source) string {
	if src.Kind == salesnav.KindSavedSearch {
		u := "https://www.linkedin.com/sales/search/people?savedSearchId=" + src.ID + "&sessionId=" + url.QueryEscape(src.SessionID)
		if src.LastViewedAt != "" {
			u += "&lastViewedAt=" + src.LastViewedAt
		}
		return u
	}
	return "https://www.linkedin.com/sales/lists/people/" + src.ID
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)

// shellQuote quotes s for sh/bash/zsh when it has anything but plainly safe characters.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return singleQuote(s)
}

// singleQuote always quotes s for sh/bash/zsh; URLs are always quoted so they read the same.
func singleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// abandon deletes a new output that holds no leads, so a rerun needs no --force. It
// reports whether it did.
func (j job) abandon() bool {
	if j.info.Resumed || j.w.Rows() > 0 {
		return false
	}
	j.w.Close()
	os.Remove(j.out)
	os.Remove(salesnavout.StatePath(j.out))
	return true
}

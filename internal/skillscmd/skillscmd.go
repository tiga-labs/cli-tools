// Package skillscmd implements `tiga skills`: installing the bundled agent skills for Claude
// Code and Codex.
package skillscmd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/tiga-labs/cli-tools/pkg/cliflags"
	"github.com/tiga-labs/cli-tools/skills"
)

// agent is a coding agent that reads SKILL.md files from <base>/<dir>/<name>/SKILL.md.
type agent struct {
	name  string // flag name: claude, codex
	label string
	dir   string // relative to the home directory or the project
}

var agents = []agent{
	{name: "claude", label: "Claude Code", dir: filepath.Join(".claude", "skills")},
	{name: "codex", label: "Codex", dir: filepath.Join(".agents", "skills")},
}

// isTerminal reports whether stdin is an interactive terminal. Tests replace it.
var isTerminal = func() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// Run implements `tiga skills <command>`.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return nil
	}
	switch args[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return nil
	case "list", "ls":
		return runList(args[1:], stdout, stderr)
	case "install":
		return runInstall(args[1:], stdin, stdout, stderr)
	case "uninstall", "rm":
		return runUninstall(args[1:], stdin, stdout, stderr)
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintf(w, `tiga skills — install the tiga agent skills for Claude Code and Codex

Usage:
  tiga skills list [--project] [--dir <path>]
  tiga skills install [--claude] [--codex] [--project] [--dir <path>]
  tiga skills uninstall [--claude] [--codex] [--project] [--dir <path>]

Skills go to ~/.claude/skills/<name>/ (Claude Code) and ~/.agents/skills/<name>/ (Codex),
or under the current directory with --project. Without --claude or --codex, install and
uninstall ask which agents to use. Run install again after upgrading tiga to update the skills; list shows "differs" when an
installed copy no longer matches this tiga.

Bundled skills: %s
`, strings.Join(skills.Names(), ", "))
}

// targetFlags are the flags shared by every skills subcommand.
type targetFlags struct {
	fs      *flag.FlagSet
	claude  *bool
	codex   *bool
	project *bool
	dir     *string
}

func bindTargetFlags(name string, withAgents bool, stderr io.Writer) *targetFlags {
	fs := flag.NewFlagSet("tiga skills "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	t := &targetFlags{fs: fs}
	if withAgents {
		t.claude = fs.Bool("claude", false, "Use Claude Code (~/.claude/skills)")
		t.codex = fs.Bool("codex", false, "Use Codex (~/.agents/skills)")
	}
	t.project = fs.Bool("project", false, "Use the current directory instead of your home directory")
	t.dir = fs.String("dir", "", "Use this base directory instead of your home directory")
	return t
}

func (t *targetFlags) parse(args []string) (bool, error) {
	_, flagArgs := cliflags.SplitPositionalAndFlags(args, map[string]bool{"--dir": true, "-dir": true})
	if err := t.fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// base returns the directory the agent skill folders live under.
func (t *targetFlags) base() (string, error) {
	switch {
	case *t.dir != "":
		return *t.dir, nil
	case *t.project:
		return os.Getwd()
	default:
		return os.UserHomeDir()
	}
}

// chosenAgents returns the agents from --claude/--codex, or asks on a terminal.
func (t *targetFlags) chosenAgents(verb string, stdin io.Reader, stdout io.Writer) ([]agent, error) {
	var out []agent
	if *t.claude {
		out = append(out, agents[0])
	}
	if *t.codex {
		out = append(out, agents[1])
	}
	if len(out) > 0 {
		return out, nil
	}
	if !isTerminal() {
		return nil, fmt.Errorf("pass --claude and/or --codex to choose where to %s the skills", verb)
	}
	fmt.Fprintf(stdout, "%s skills for: [1] Claude Code  [2] Codex  [3] both (default 3): ", strings.ToUpper(verb[:1])+verb[1:])
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	switch strings.TrimSpace(line) {
	case "1":
		return agents[:1], nil
	case "2":
		return agents[1:], nil
	case "", "3":
		return agents, nil
	default:
		return nil, fmt.Errorf("unknown choice %q (expected 1, 2 or 3)", strings.TrimSpace(line))
	}
}

func skillPath(base string, a agent, name string) string {
	return filepath.Join(base, a.dir, name, "SKILL.md")
}

func runInstall(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	t := bindTargetFlags("install", true, stderr)
	if ok, err := t.parse(args); !ok {
		return err
	}
	base, err := t.base()
	if err != nil {
		return err
	}
	chosen, err := t.chosenAgents("install", stdin, stdout)
	if err != nil {
		return err
	}
	for _, a := range chosen {
		for _, name := range skills.Names() {
			data, err := skills.Read(name)
			if err != nil {
				return err
			}
			path := skillPath(base, a, name)
			status, err := writeSkill(path, data)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "%-9s %-12s %s\n", status, a.label, path)
		}
	}
	return nil
}

// writeSkill writes data to path through a temp file and reports installed, updated, or unchanged.
func writeSkill(path string, data []byte) (string, error) {
	existing, err := os.ReadFile(path)
	status := "installed"
	switch {
	case err == nil && bytes.Equal(existing, data):
		return "unchanged", nil
	case err == nil:
		status = "updated"
	case !errors.Is(err, os.ErrNotExist):
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".SKILL.md.*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return status, os.Rename(tmp.Name(), path)
}

func runUninstall(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	t := bindTargetFlags("uninstall", true, stderr)
	if ok, err := t.parse(args); !ok {
		return err
	}
	base, err := t.base()
	if err != nil {
		return err
	}
	chosen, err := t.chosenAgents("uninstall", stdin, stdout)
	if err != nil {
		return err
	}
	for _, a := range chosen {
		for _, name := range skills.Names() {
			path := skillPath(base, a, name)
			if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
				continue
			} else if err != nil {
				return err
			}
			_ = os.Remove(filepath.Dir(path)) // only succeeds if the folder is now empty
			fmt.Fprintf(stdout, "%-9s %-12s %s\n", "removed", a.label, path)
		}
	}
	return nil
}

func runList(args []string, stdout, stderr io.Writer) error {
	t := bindTargetFlags("list", false, stderr)
	if ok, err := t.parse(args); !ok {
		return err
	}
	base, err := t.base()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SKILL\tAGENT\tSTATUS\tPATH")
	for _, name := range skills.Names() {
		data, err := skills.Read(name)
		if err != nil {
			return err
		}
		for _, a := range agents {
			path := skillPath(base, a, name)
			status := "not installed"
			if existing, err := os.ReadFile(path); err == nil {
				status = "installed"
				if !bytes.Equal(existing, data) {
					status = "differs"
				}
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", name, a.label, status, path)
		}
	}
	return tw.Flush()
}

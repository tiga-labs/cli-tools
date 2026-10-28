package signalscmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/tiga-labs/cli-tools/pkg/signals"
)

const skillPath = "../../skills/signals-cli/SKILL.md"

var (
	fencedBlock = regexp.MustCompile("(?s)```(\\w*)\\n(.*?)```")
	helpFlag    = regexp.MustCompile(`(?m)^\s+-([a-z0-9][a-z0-9-]*)`)
	usedFlag    = regexp.MustCompile(`(?:^|\s)--?([a-z][a-z0-9-]*)`)
	quoted      = regexp.MustCompile(`"[^"]*"|'[^']*'`)
	inlineCode  = regexp.MustCompile("`([^`\n]+)`")
	proseFlag   = regexp.MustCompile(`(?:^|[\s(/])--([a-z][a-z0-9-]*)`)
)

// commandFlags returns the flags a subcommand accepts, read from its own --help output.
func commandFlags(t *testing.T, cmd string) map[string]bool {
	t.Helper()
	var stderr bytes.Buffer
	if err := runCLI(context.Background(), []string{cmd, "--help"}, strings.NewReader(""), io.Discard, &stderr); err != nil {
		t.Fatal(err)
	}
	flags := map[string]bool{}
	for _, m := range helpFlag.FindAllStringSubmatch(stderr.String(), -1) {
		flags[m[1]] = true
	}
	return flags
}

// TestSkillExamples keeps skills/signals-cli/SKILL.md honest: every JSON definition parses, and
// every flag in a `signals run` / `signals csv` example exists on that command.
func TestSkillExamples(t *testing.T) {
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	definitions, commands := checkDocExamples(t, string(data))
	if definitions < 5 || commands < 6 {
		t.Fatalf("skill has too few checked examples: %d definitions, %d commands", definitions, commands)
	}
}

// TestReadmeSignalsExamples does the same for the README's signals section.
func TestReadmeSignalsExamples(t *testing.T) {
	data, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(data)
	start := strings.Index(readme, "\n## signals")
	if start < 0 {
		t.Fatal("README has no ## signals section")
	}
	if _, commands := checkDocExamples(t, readme[start:]); commands < 3 {
		t.Fatalf("README signals section has %d checked commands", commands)
	}
}

func checkDocExamples(t *testing.T, doc string) (definitions, commands int) {
	t.Helper()
	flags := map[string]map[string]bool{"run": commandFlags(t, "run"), "csv": commandFlags(t, "csv")}
	data := []byte(doc)
	for _, block := range fencedBlock.FindAllStringSubmatch(string(data), -1) {
		lang, body := block[1], block[2]
		if lang == "json" && strings.Contains(body, `"computed_config"`) {
			definitions++
			if _, err := signals.Parse([]byte(body)); err != nil {
				t.Errorf("skill definition does not parse: %v\n%s", err, body)
			}
			continue
		}
		// Join backslash-continued lines, then check each signals run/csv command.
		joined := strings.ReplaceAll(body, "\\\n", " ")
		for _, line := range strings.Split(joined, "\n") {
			line = strings.TrimSpace(line)
			for _, cmd := range []string{"run", "csv"} {
				idx := strings.Index(line, "signals "+cmd+" ")
				if idx < 0 && !strings.HasSuffix(line, "signals "+cmd) {
					continue
				}
				commands++
				rest := quoted.ReplaceAllString(line[idx+len("signals "+cmd):], `""`) // ignore quoted values
				if pipe := strings.IndexAny(rest, "|>#"); pipe >= 0 {
					rest = rest[:pipe] // stop at a pipe, redirect, or comment
				}
				for _, m := range usedFlag.FindAllStringSubmatch(rest, -1) {
					if !flags[cmd][m[1]] {
						t.Errorf("skill uses unknown flag --%s in: %s", m[1], line)
					}
				}
			}
		}
	}
	// Flags named anywhere in inline code (tables, prose) must exist on some subcommand.
	// help is on every command; research-company is enrich's flag, named when pointing to enrich-cli.
	all := map[string]bool{"help": true, "research-company": true}
	for _, cmd := range []string{"run", "csv", "keys", "cache"} {
		for f := range commandFlags(t, cmd) {
			all[f] = true
		}
	}
	for _, m := range inlineCode.FindAllStringSubmatch(doc, -1) {
		for _, f := range proseFlag.FindAllStringSubmatch(m[1], -1) {
			if !all[f[1]] {
				t.Errorf("doc names unknown flag --%s in `%s`", f[1], m[1])
			}
		}
	}
	return definitions, commands
}

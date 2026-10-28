package salesnavcmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
)

const skillPath = "../../skills/salesnav-cli/SKILL.md"

var (
	fencedBlock = regexp.MustCompile("(?s)```\\w*\\n(.*?)```")
	helpFlag    = regexp.MustCompile(`(?m)^\s+-([a-z0-9][a-z0-9-]*)`)
	usedFlag    = regexp.MustCompile(`(?:^|\s)--?([a-z][a-z0-9-]*)`)
	quoted      = regexp.MustCompile(`"[^"]*"|'[^']*'`)
	inlineCode  = regexp.MustCompile("`([^`\n]+)`")
	proseFlag   = regexp.MustCompile(`(?:^|[\s(/])--([a-z][a-z0-9-]*)`)
)

// ownFlags are the flags `tiga salesnav-list-import --help` lists.
func ownFlags(t *testing.T) map[string]bool {
	t.Helper()
	var stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--help"}, io.Discard, &stderr); err != nil {
		t.Fatal(err)
	}
	flags := map[string]bool{"help": true}
	for _, m := range helpFlag.FindAllStringSubmatch(stderr.String(), -1) {
		flags[m[1]] = true
	}
	return flags
}

// checkDocFlags fails on any flag a doc uses that the commands do not have, and returns how
// many `tiga salesnav-list-import` command lines it checked. The hand-off to enrich may
// use only -i and -o.
func checkDocFlags(t *testing.T, doc string) int {
	t.Helper()
	own := ownFlags(t)
	enrich := map[string]bool{"i": true, "o": true}
	commands := 0
	for _, block := range fencedBlock.FindAllStringSubmatch(doc, -1) {
		for _, line := range strings.Split(strings.ReplaceAll(block[1], "\\\n", " "), "\n") {
			line = strings.TrimSpace(line)
			var flags map[string]bool
			var rest string
			switch {
			case strings.HasPrefix(line, "tiga salesnav-list-import"):
				commands++
				flags, rest = own, strings.TrimPrefix(line, "tiga salesnav-list-import")
			case strings.HasPrefix(line, "tiga enrich csv"):
				flags, rest = enrich, strings.TrimPrefix(line, "tiga enrich csv")
			default:
				continue
			}
			rest = quoted.ReplaceAllString(rest, `""`)
			if cut := strings.IndexAny(rest, "|>#&"); cut >= 0 {
				rest = rest[:cut]
			}
			for _, m := range usedFlag.FindAllStringSubmatch(rest, -1) {
				if !flags[m[1]] {
					t.Errorf("unknown flag -%s in: %s", m[1], line)
				}
			}
		}
	}
	// Flags named in inline code (tables, prose) must be the command's own.
	for _, m := range inlineCode.FindAllStringSubmatch(doc, -1) {
		for _, f := range proseFlag.FindAllStringSubmatch(m[1], -1) {
			if !own[f[1]] {
				t.Errorf("doc names unknown flag --%s in `%s`", f[1], m[1])
			}
		}
	}
	return commands
}

// TestSkillFlagsExist keeps skills/salesnav-cli/SKILL.md in step with the command.
func TestSkillFlagsExist(t *testing.T) {
	data, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	if n := checkDocFlags(t, string(data)); n < 4 {
		t.Fatalf("skill has %d checked salesnav-list-import commands, want at least 4", n)
	}
}

// TestReadmeSalesNavExamples does the same for the README's salesnav-list-import section.
func TestReadmeSalesNavExamples(t *testing.T) {
	data, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(data)
	start := strings.Index(readme, "\n## salesnav-list-import")
	if start < 0 {
		t.Fatal("README has no ## salesnav-list-import section")
	}
	section := readme[start+1:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	if n := checkDocFlags(t, section); n < 3 {
		t.Fatalf("README section has %d checked commands, want at least 3", n)
	}
}

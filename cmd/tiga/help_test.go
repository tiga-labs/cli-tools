package main

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
)

var (
	helpFlag = regexp.MustCompile(`(?m)^\s+-([a-z0-9][a-z0-9-]*)`)
	usedFlag = regexp.MustCompile(`(?:^|[\s(\[|])--?([a-z][a-z0-9-]*)`)
	quoted   = regexp.MustCompile(`"[^"]*"`)
)

// commandFlags returns the flags `tiga <cmd...> --help` lists.
func commandFlags(t *testing.T, cmd ...string) map[string]bool {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := runCLI(context.Background(), append(cmd, "--help"), strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("%v --help: %v", cmd, err)
	}
	out := stderr.String() + stdout.String()
	flags := map[string]bool{}
	for _, m := range helpFlag.FindAllStringSubmatch(out, -1) {
		flags[m[1]] = true
	}
	if len(flags) == 0 { // a hand-written usage (tiga keys) names its flags inline
		for _, m := range usedFlag.FindAllStringSubmatch(out, -1) {
			flags[m[1]] = true
		}
	}
	if len(flags) == 0 {
		t.Fatalf("%v --help lists no flags", cmd)
	}
	return flags
}

func union(sets ...map[string]bool) map[string]bool {
	out := map[string]bool{"help": true}
	for _, s := range sets {
		for k := range s {
			out[k] = true
		}
	}
	return out
}

// helpSection returns the overview text from the heading start up to the next heading.
func helpSection(t *testing.T, start string) string {
	t.Helper()
	i := strings.Index(helpText, "\n"+start)
	if i < 0 {
		t.Fatalf("help has no %s section", start)
	}
	rest := helpText[i+1:]
	if j := regexp.MustCompile(`\n[A-Z]{4,} `).FindStringIndex(rest[1:]); j != nil {
		rest = rest[:j[0]+1]
	}
	return rest
}

// TestHelpFlagsExist keeps `tiga help` honest: every flag a section names exists on one of that
// group's commands, and every example uses only its own command's flags.
func TestHelpFlagsExist(t *testing.T) {
	person, ecsv := commandFlags(t, "enrich", "person"), commandFlags(t, "enrich", "csv")
	run, scsv, cache := commandFlags(t, "signals", "run"), commandFlags(t, "signals", "csv"), commandFlags(t, "signals", "cache")
	keys := commandFlags(t, "keys")
	install, list := commandFlags(t, "skills", "install"), commandFlags(t, "skills", "list")
	salesnav := commandFlags(t, "salesnav-list-import")
	for _, f := range []string{"o", "max", "resume", "force", "chrome-user-data-dir"} {
		if !salesnav[f] {
			t.Errorf("salesnav-list-import --help does not list -%s", f)
		}
	}

	sections := map[string]map[string]bool{
		"ENRICH":    union(person, ecsv),
		"SIGNALS":   union(run, scsv, cache),
		"KEYS":      union(keys),
		"SKILLS":    union(install, list),
		"SALES NAV": union(salesnav),
	}
	for name, flags := range sections {
		text := quoted.ReplaceAllString(helpSection(t, name+" "), `""`)
		for _, m := range usedFlag.FindAllStringSubmatch(text, -1) {
			if !flags[m[1]] {
				t.Errorf("%s section names unknown flag -%s", name, m[1])
			}
		}
	}

	examples := map[string]map[string]bool{
		"tiga enrich person ": person, "tiga enrich csv ": ecsv,
		"tiga signals run ": run, "tiga signals csv ": scsv,
		"tiga salesnav-list-import ": salesnav,
	}
	checked := 0
	for _, line := range strings.Split(helpText, "\n") {
		line = strings.TrimSpace(line)
		for prefix, flags := range examples {
			// Usage lines contain <placeholders>; examples are the concrete lines.
			if !strings.HasPrefix(line, prefix) || strings.Contains(line, "<") {
				continue
			}
			checked++
			for _, m := range usedFlag.FindAllStringSubmatch(quoted.ReplaceAllString(line, `""`), -1) {
				if !flags[m[1]] {
					t.Errorf("example uses unknown flag -%s: %s", m[1], line)
				}
			}
		}
	}
	if checked < 6 {
		t.Errorf("checked %d examples, want at least 6", checked)
	}
}

func TestHelpDrillsIntoFullFlags(t *testing.T) {
	for _, cmd := range [][]string{{"signals", "run"}, {"enrich", "person"}, {"keys"}, {"skills", "install"}} {
		out, _, err := cli(t, "", append([]string{"help"}, cmd...)...)
		if err != nil || !strings.Contains(out, "-config") && !strings.Contains(out, "-claude") {
			t.Errorf("help %v: err=%v out=%q", cmd, err, out)
		}
	}
}

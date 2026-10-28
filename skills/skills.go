// Package skills embeds the agent skills that ship with tiga, so `tiga skills install` can
// write them for Claude Code and Codex without the repo being present.
package skills

import (
	"embed"
	"io/fs"
	"sort"
)

// FS holds every bundled skill as <name>/SKILL.md.
//
//go:embed */SKILL.md
var FS embed.FS

// Names returns the bundled skill names (their folder names), sorted.
func Names() []string {
	entries, _ := fs.ReadDir(FS, ".")
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// Read returns the SKILL.md content of the named skill.
func Read(name string) ([]byte, error) {
	return FS.ReadFile(name + "/SKILL.md")
}

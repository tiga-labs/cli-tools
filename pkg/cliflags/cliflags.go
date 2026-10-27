// Package cliflags holds the flag-parsing helpers shared by every Tiga CLI tool.
package cliflags

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
)

// SplitPositionalAndFlags separates positional arguments from flags so Go's standard
// flag package doesn't stop parsing flags when a positional argument appears first.
// valueFlags lists the flags (with dashes) that consume the following argument.
func SplitPositionalAndFlags(args []string, valueFlags map[string]bool) (positional []string, flags []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, arg)
			if !strings.Contains(arg, "=") && valueFlags[arg] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		} else {
			positional = append(positional, arg)
		}
	}
	return positional, flags
}

// Toggle is a --name / --no-name flag pair.
type Toggle struct {
	on, off *bool
}

// BindToggle registers --name (default def) and --no-name on fs.
func BindToggle(fs *flag.FlagSet, name string, def bool, onUsage, offUsage string) Toggle {
	return Toggle{
		on:  fs.Bool(name, def, onUsage),
		off: fs.Bool("no-"+name, false, offUsage),
	}
}

// Value reports whether the feature is enabled; --no-name always wins.
func (t Toggle) Value() bool {
	return *t.on && !*t.off
}

// ResolveVersion returns the ldflags version if set, otherwise the main module version from
// the binary's build info. Both are normalized to the release format ("v0.1.0" → "0.1.0").
func ResolveVersion(ldflagsVersion string, info *debug.BuildInfo, ok bool) string {
	if ldflagsVersion != "" && ldflagsVersion != "dev" {
		return strings.TrimPrefix(ldflagsVersion, "v")
	}
	if ok && info != nil {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return strings.TrimPrefix(v, "v")
		}
	}
	return "dev"
}

// ReadKeyFromStdin reads an API key from the first line of r, e.g. `pbpaste | <tool> keys set x -`.
func ReadKeyFromStdin(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("failed to read API key from stdin: %w", err)
	}
	key := strings.TrimSpace(line)
	if key == "" {
		return "", errors.New("no API key received on stdin")
	}
	return key, nil
}

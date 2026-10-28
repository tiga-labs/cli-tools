package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/tiga-labs/cli-tools/internal/enrichcmd"
	"github.com/tiga-labs/cli-tools/internal/salesnavcmd"
	"github.com/tiga-labs/cli-tools/internal/signalscmd"
	"github.com/tiga-labs/cli-tools/internal/skillscmd"
	"github.com/tiga-labs/cli-tools/pkg/cliflags"
	"github.com/tiga-labs/cli-tools/pkg/enrich"
	"github.com/tiga-labs/cli-tools/pkg/keystore"
	"github.com/tiga-labs/cli-tools/pkg/signals"
)

// version is set at release time via -ldflags "-X main.version=...". Builds without it
// (e.g. `go install ...@v0.1.0`) fall back to the module version embedded by the Go toolchain.
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runCLI(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(exitCode(err))
	}
}

// exitCode is 0 for nil, the error's own ExitCode() when it has one (salesnav-list-import
// exits 2 when LinkedIn stops a run), and 1 otherwise.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return 1
}

func runCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printRootUsage(stderr)
		return nil
	}

	switch args[0] {
	case "-h", "--help":
		printHelp(stdout)
		return nil
	case "help":
		return runHelp(ctx, args[1:], stdin, stdout)
	case "-v", "--version", "version":
		info, ok := debug.ReadBuildInfo()
		fmt.Fprintf(stdout, "tiga %s\n", cliflags.ResolveVersion(version, info, ok))
		return nil
	case "enrich":
		return enrichcmd.Run(ctx, args[1:], stdin, stdout, stderr)
	case "signals":
		return signalscmd.Run(ctx, args[1:], stdin, stdout, stderr)
	case "skills":
		return skillscmd.Run(ctx, args[1:], stdin, stdout, stderr)
	case "salesnav-list-import":
		return salesnavcmd.Run(ctx, args[1:], stdout, stderr)
	case "keys", "key":
		return cliflags.RunKeys(cliflags.KeysSpec{
			Tool:    "tiga",
			Vendors: mergeVendors(enrich.Vendors, signals.Vendors),
			VendorHelp: `  Enrich waterfall: findymail, prospeo, apollo, lusha, seamless, dropcontact, hunter, datagma, leadiq
  Enrich features:  renidly, zerobounce, openai
  Signals:          openai (gpt signals, name cleaning), linkdapi (LinkedIn data)`,
		}, args[1:], stdin, stdout, stderr)
	default:
		printRootUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// mergeVendors concatenates vendor registries in order, keeping the first entry for a name
// that appears in more than one (openai is used by both tools).
func mergeVendors(lists ...[]keystore.Vendor) []keystore.Vendor {
	seen := map[string]bool{}
	var out []keystore.Vendor
	for _, list := range lists {
		for _, v := range list {
			if seen[v.Name] {
				continue
			}
			seen[v.Name] = true
			out = append(out, v)
		}
	}
	return out
}

// logo is printed above the root usage and `tiga help`.
const logo = `
████████ ██  ██████   █████       ██████ ██      ██
   ██    ██ ██       ██   ██     ██      ██      ██
   ██    ██ ██   ███ ███████     ██      ██      ██
   ██    ██ ██    ██ ██   ██     ██      ██      ██
   ██    ██  ██████  ██   ██      ██████ ███████ ██
`

func printRootUsage(w io.Writer) {
	fmt.Fprint(w, logo+"\n")
	fmt.Fprintln(w, `tiga — contact enrichment and AI signals from the command line

Usage:
  tiga <command> [flags]

Commands:
  enrich      Waterfall contact enrichment (person, csv)
  signals     Run AI signals on a contact, an account, or a CSV (run, csv, cache)
  salesnav-list-import  Import a Sales Navigator lead list or saved search from your Chrome
  keys        Manage vendor API keys for every command (list, set, rm)
  skills      Install the tiga skills for Claude Code and Codex (list, install, uninstall)
  version     Print the tiga version

Run "tiga help" for every command and its main flags, or "tiga help <command>" for full flags.`)
}

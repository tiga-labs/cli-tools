package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/tiga-labs/cli-tools/internal/enrichcmd"
	"github.com/tiga-labs/cli-tools/internal/signalscmd"
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
		os.Exit(1)
	}
}

func runCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printRootUsage(stderr)
		return nil
	}

	switch args[0] {
	case "-h", "--help", "help":
		printRootUsage(stdout)
		return nil
	case "-v", "--version", "version":
		info, ok := debug.ReadBuildInfo()
		fmt.Fprintf(stdout, "tiga %s\n", cliflags.ResolveVersion(version, info, ok))
		return nil
	case "enrich":
		return enrichcmd.Run(ctx, args[1:], stdin, stdout, stderr)
	case "signals":
		return signalscmd.Run(ctx, args[1:], stdin, stdout, stderr)
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

func printRootUsage(w io.Writer) {
	fmt.Fprintln(w, `tiga — contact enrichment and AI signals from the command line

Usage:
  tiga <command> [flags]

Commands:
  enrich      Waterfall contact enrichment (person, csv)
  signals     Run AI signals on a contact, an account, or a CSV (run, csv, cache)
  keys        Manage vendor API keys for every command (list, set, rm)
  version     Print the tiga version

Run "tiga <command> --help" for more information on a command.`)
}

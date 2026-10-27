package cliflags

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/tiga-labs/cli-tools/pkg/keystore"
)

// KeysSpec describes the tool running the `keys` subcommand.
type KeysSpec struct {
	Tool       string            // binary name, used in usage and messages
	Vendors    []keystore.Vendor // the tool's vendor registry
	VendorHelp string            // vendor list shown under "Vendors:" in usage
}

// RunKeys implements `<tool> keys list|set|rm` against the shared key file.
func RunKeys(spec KeysSpec, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet(spec.Tool+" keys", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "Path to config file (default: ~/.config/tiga/config.json)")
	jsonOut := fs.Bool("json", false, "Output key list as JSON")

	fs.Usage = func() {
		fmt.Fprintf(stderr, `Usage:
  %[1]s keys list [--json] [--config <path>]
  %[1]s keys set <vendor> <api-key> [--config <path>]
  %[1]s keys set <vendor> - [--config <path>]   (read the key from stdin; keeps it out of shell history)
  %[1]s keys rm <vendor> [--config <path>]

Vendors:
%[2]s
`, spec.Tool, spec.VendorHelp)
	}

	// Allow flags before or after positional arguments
	posArgs, flagArgs := SplitPositionalAndFlags(args, map[string]bool{"--config": true, "-config": true})
	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	action := "list"
	if len(posArgs) > 0 {
		action = strings.ToLower(posArgs[0])
	}

	cfg, err := keystore.Load(*configPath, spec.Vendors)
	if err != nil {
		return err
	}

	switch action {
	case "list", "ls":
		statuses := cfg.ListKeyStatuses()
		if *jsonOut {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(statuses)
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "VENDOR\tCATEGORY\tCONFIGURED\tSOURCE\tKEY")
		for _, s := range statuses {
			cfgStr := "no"
			if s.Configured {
				cfgStr = "yes"
			}
			mask := s.MaskedKey
			if mask == "" {
				mask = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.Vendor, s.Category, cfgStr, s.Source, mask)
		}
		return tw.Flush()

	case "set", "add":
		if len(posArgs) < 3 {
			fs.Usage()
			return fmt.Errorf("usage: %s keys set <vendor> <api-key|->", spec.Tool)
		}
		vendor := posArgs[1]
		key := posArgs[2]
		if key == "-" {
			if key, err = ReadKeyFromStdin(stdin); err != nil {
				return err
			}
		}
		if err := cfg.SetKey(vendor, key); err != nil {
			return err
		}
		if err := keystore.Save(*configPath, cfg); err != nil {
			return err
		}
		norm, _ := cfg.NormalizeVendor(vendor)
		fmt.Fprintf(stdout, "Saved API key for %s (%s)\n", norm, keystore.MaskKey(key))
		return nil

	case "rm", "remove", "delete", "del":
		if len(posArgs) < 2 {
			fs.Usage()
			return fmt.Errorf("usage: %s keys rm <vendor>", spec.Tool)
		}
		vendor := posArgs[1]
		if err := cfg.RemoveKey(vendor); err != nil {
			return err
		}
		if err := keystore.Save(*configPath, cfg); err != nil {
			return err
		}
		norm, _ := cfg.NormalizeVendor(vendor)
		fmt.Fprintf(stdout, "Removed API key for %s from config\n", norm)
		return nil

	default:
		fs.Usage()
		return fmt.Errorf("unknown keys action %q (expected: list, set, rm)", action)
	}
}

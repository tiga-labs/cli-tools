package enrichcmd

import (
	"context"
	"io"

	"github.com/tiga-labs/cli-tools/pkg/cliflags"
	"github.com/tiga-labs/cli-tools/pkg/enrich"
)

// runCLI runs Run, routing "keys" to the shared key command the way `tiga keys` does.
func runCLI(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "keys" {
		return cliflags.RunKeys(cliflags.KeysSpec{Tool: "tiga", Vendors: enrich.Vendors}, args[1:], stdin, stdout, stderr)
	}
	return Run(ctx, args, stdin, stdout, stderr)
}

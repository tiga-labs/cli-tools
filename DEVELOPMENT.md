# Development

This guide is for working on `cli-tools` itself. To *use* `tiga`, see the [README](README.md).

## Repo layout

```text
cmd/
  tiga/           # The tiga binary: dispatches to enrich/signals, owns `tiga keys` and `tiga version`
internal/
  enrichcmd/      # `tiga enrich` subcommands (flags, CSV file handling)
  signalscmd/     # `tiga signals` subcommands (flags, CSV file handling, e2e tests)
pkg/
  enrich/         # Waterfall enrichment engine and vendor adapters
  freeemail/      # Free/disposable email domain detector
  urlutil/        # URL, domain, LinkedIn handle/slug, and hash utilities
skills/
  enrich-cli/     # Claude Code / AI agent skill for `tiga enrich`
  signals-cli/    # Claude Code / AI agent skill for `tiga signals`
.github/workflows/release.yml   # Publishes GitHub Releases on v* tags
.goreleaser.yaml                # Release build configuration
install.sh                      # One-line installer (attached to every release)
```

## Prerequisites

- Go, at the version in [`go.mod`](go.mod).
- [GoReleaser](https://goreleaser.com/install/) v2, needed only for `make release-snapshot`.

## Build and test

```bash
make build                  # build bin/tiga for this machine
make build-mac-x86-tiga     # build tiga for Intel macOS into bin/tiga-darwin-amd64
make test                   # unit + integration tests (go test -race -cover ./...)
```

`make test` never calls real vendors. Vendor HTTP calls are stubbed through `httpx.Transport`.

## End-to-end tests (live vendors)

These tests call the real vendor APIs and **spend credits**. They never run with `make test`. You must use the `e2e` build tag and set `ENRICH_E2E=1`.

1. Copy [`pkg/enrich/testdata/e2e/contacts.example.json`](pkg/enrich/testdata/e2e/contacts.example.json) to `contacts.json` in the same folder and add 2–3 real contacts. This file is gitignored.
2. Give the tests your API keys in one of two ways:
   - Run `tiga keys set <vendor> -`.
   - Put `*_API_KEY=...` lines in a gitignored `.env.e2e` at the repo root.

   Any vendor without a key is skipped.
3. Run `make test-e2e`.

The tests check only the shape of each result: status, email syntax, canonical LinkedIn URLs, that a source field is set, and that no API key appears in errors. They do not check exact values. At the end, the run prints the number of HTTP calls made to each vendor.

| Variable | Default | Purpose |
|---|---|---|
| `ENRICH_E2E_MAX_CONTACTS` | `3` | Maximum number of fixture contacts to use |
| `ENRICH_E2E_PHONE` | off | Set to `1` to also request phone numbers (phone lookups cost more credits) |
| `ENRICH_E2E_FIXTURE` | `testdata/e2e/contacts.json` | Contacts file to use |
| `ENRICH_E2E_CONFIG` | `~/.config/tiga/config.json` | Config file to read keys from |

To test one vendor:

```bash
ENRICH_E2E=1 go test -tags e2e -run 'E2EWaterfallVendor/hunter' -count=1 -v ./pkg/enrich/
```

### signals

The signals e2e tests live in `internal/signalscmd/e2e_test.go`. They need the `e2e` build tag and `SIGNALS_E2E=1`, so they never run with `make test`.

1. Give the tests your keys: `tiga keys set openai -` (and `tiga keys set linkdapi -`), or `OPENAI_API_KEY` / `LINKDAPI_API_KEY` lines in `.env.e2e`. Without a linkdapi key, the LinkedIn tests are skipped.
2. Run `make test-e2e-signals`.

Each test uses a fresh cache folder, so it always calls the vendors. One run costs about 6 OpenAI requests, plus up to 25 classifier and 25 research-model calls for the company-posts test, and about 7 linkdapi credits.

| Variable | Default | Purpose |
|---|---|---|
| `SIGNALS_E2E_CONFIG` | `~/.config/tiga/config.json` | Config file to read keys from |

The Stripe test expects a "no recent funding" answer, as checked on 2026-10-28. If Stripe announces a raise, update `TestE2EStripeFundingDefaultClassifiesNo`.

## Releasing

Releases are published to [GitHub Releases](https://github.com/tiga-labs/cli-tools/releases) by GoReleaser. To cut a release, tag `master` and push the tag:

```bash
git tag -a v0.2.0 -m v0.2.0
git push origin v0.2.0
```

The [`release`](.github/workflows/release.yml) workflow then:
- runs the tests;
- builds `darwin`/`linux` × `amd64`/`arm64` binaries;
- uploads them as `tiga-<os>-<arch>`, together with `checksums.txt` and `install.sh`.

The version printed by `tiga --version` comes from the tag. Asset names have no version in them, so `.../releases/latest/download/<asset>` always points at the newest release, and that is the URL the installer uses.

To try the release build locally without publishing anything:

```bash
make release-snapshot       # builds into dist/
```

## Adding a new command group

1. Create `internal/<name>cmd/` with a `Run(ctx, args, stdin, stdout, stderr) error` entry point (library code goes under `pkg/<name>/`).
2. Add a `case "<name>"` to `runCLI` in [`cmd/tiga/main.go`](cmd/tiga/main.go) and a line to its root usage.
3. If it needs API keys, add its vendor registry to the `mergeVendors` call there, so `tiga keys` manages them.

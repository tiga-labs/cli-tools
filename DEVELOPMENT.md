# Development

This guide is for working on `cli-tools` itself. To *use* `enrich`, see the [README](README.md).

## Repo layout

```text
cmd/
  enrich/         # The enrich CLI entrypoint (flags, subcommands, CSV file handling)
  <future-tool>/  # Add new CLI entrypoints here; the Makefile builds all cmd/* automatically
pkg/
  enrich/         # Waterfall enrichment engine and vendor adapters
  freeemail/      # Free/disposable email domain detector
  urlutil/        # URL, domain, LinkedIn handle/slug, and hash utilities
skills/
  enrich-cli/     # Claude Code / AI agent skill for the enrich CLI
.github/workflows/release.yml   # Publishes GitHub Releases on v* tags
.goreleaser.yaml                # Release build configuration
install.sh                      # One-line installer (attached to every release)
```

## Prerequisites

- Go, at the version in [`go.mod`](go.mod).
- [GoReleaser](https://goreleaser.com/install/) v2, needed only for `make release-snapshot`.

## Build and test

```bash
make build                  # build every tool in cmd/* into bin/<tool> for this machine
make build-enrich-mac-x86   # build enrich for Intel macOS into bin/enrich-darwin-amd64
make test                   # unit + integration tests (go test -race -cover ./...)
```

`make test` never calls real vendors. Vendor HTTP calls are stubbed through `httpx.Transport`.

## End-to-end tests (live vendors)

These tests call the real vendor APIs and **spend credits**. They never run with `make test`. You must use the `e2e` build tag and set `ENRICH_E2E=1`.

1. Copy [`pkg/enrich/testdata/e2e/contacts.example.json`](pkg/enrich/testdata/e2e/contacts.example.json) to `contacts.json` in the same folder and add 2–3 real contacts. This file is gitignored.
2. Give the tests your API keys in one of two ways:
   - Run `enrich keys set <vendor> -`.
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

## Releasing

Releases are published to [GitHub Releases](https://github.com/tiga-labs/cli-tools/releases) by GoReleaser. To cut a release, tag `master` and push the tag:

```bash
git tag -a v0.2.0 -m v0.2.0
git push origin v0.2.0
```

The [`release`](.github/workflows/release.yml) workflow then:
- runs the tests;
- builds `darwin`/`linux` × `amd64`/`arm64` binaries;
- uploads them as `enrich-<os>-<arch>`, together with `checksums.txt` and `install.sh`.

The version printed by `enrich --version` comes from the tag. Asset names have no version in them, so `.../releases/latest/download/<asset>` always points at the newest release, and that is the URL the installer uses.

To try the release build locally without publishing anything:

```bash
make release-snapshot       # builds into dist/
```

## Adding a new tool

1. Create `cmd/<tool-name>/main.go` (and optional library code under `pkg/<tool-name>/`).
2. `make build` will automatically discover `cmd/<tool-name>` and build it for your machine.
3. Add a matching `builds` entry in `.goreleaser.yaml` so the tool is included in releases. `install.sh` can install it with `TOOL=<tool-name>`.

# Tiga CLI Tools (`cli-tools`)

Standalone, zero-dependency Go command-line tools for GTM workflows and data operations.

## Layout

```text
cmd/
  enrich/         # Standalone waterfall contact enrichment CLI
  <future-tool>/  # Add new CLI entrypoints here; Makefile builds all cmd/* automatically
pkg/
  enrich/         # Core waterfall enrichment engine & vendor adapters
  freeemail/      # Free/disposable email domain detector
  urlutil/        # URL, domain, LinkedIn handle/slug, and hash utilities
skills/
  enrich-cli/     # Claude Code / AI agent skill for the `enrich` CLI
```

## Available Tools

### 1. `enrich` — Waterfall Contact Enrichment CLI
Run waterfall enrichment against CSVs of contact data or a single contact across 9 data providers (`findymail`, `prospeo`, `apollo`, `lusha`, `seamless`, `dropcontact`, `hunter`, `datagma`, `leadiq`), plus:
- **LinkedIn URN / Handle resolution** via [Renidly](https://renidly.com) (`--resolve-urn`)
- **Email verification** via ZeroBounce (`--verify-email`)
- **AI name & company cleaning** via OpenAI (`--clean-names`)
- **Company LinkedIn / domain research** (`--research-company`)

```bash
# Manage vendor API keys (~/.config/enrich/config.json)
enrich keys list
enrich keys set renidly rnd-...
enrich keys set findymail fm-...

# Enrich a single person
enrich person --first-name "Jane" --last-name "Doe" --domain "acme.com" --format table

# Enrich a CSV batch
enrich csv -i contacts.csv -o enriched.csv --concurrency 5 --verify-email
```

See [`skills/enrich-cli/SKILL.md`](skills/enrich-cli/SKILL.md) for comprehensive workflow examples.

---

## Building & Releasing

```bash
# Build all tools under cmd/* for your current machine into bin/<tool>
make build

# Build enrich specifically for x86_64 macOS into bin/enrich-darwin-amd64
make build-enrich-mac-x86

# Cross-compile all tools under cmd/* for darwin/{amd64,arm64} and linux/{amd64,arm64} into dist/
make build-release

# Run all unit and integration tests
make test
```

### Adding a New Tool
1. Create `cmd/<tool-name>/main.go` (and optional library code under `pkg/<tool-name>/`).
2. `make build` and `make build-release` will automatically discover `cmd/<tool-name>` and build binaries for all target platforms.
3. Add a matching entry in `.goreleaser.yaml` if releasing via GoReleaser.

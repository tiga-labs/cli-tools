# tiga CLI Makefile
# Builds every binary under cmd/* (today just cmd/tiga)

TOOLS := $(notdir $(wildcard cmd/*))
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build test test-e2e test-e2e-signals clean release-snapshot $(addprefix build-,$(TOOLS)) $(addprefix build-mac-x86-,$(TOOLS))

all: build

# Build all tools in cmd/* for the host OS/architecture into bin/<tool>
build: $(addprefix build-,$(TOOLS))

$(addprefix build-,$(TOOLS)): build-%:
	@mkdir -p bin
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/$* ./cmd/$*

# Build any binary for x86_64 macOS: e.g. `make build-mac-x86-tiga`
$(addprefix build-mac-x86-,$(TOOLS)): build-mac-x86-%:
	@mkdir -p bin
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/$*-darwin-amd64 ./cmd/$*

# Local dry run of the GoReleaser release (cross-compiles into dist/, publishes nothing).
# Real releases are published to GitHub Releases by CI when a v* tag is pushed.
release-snapshot:
	goreleaser release --snapshot --clean

test:
	go test -race -cover ./...

# Live end-to-end tests against real vendors (spends API credits). Sources .env.e2e if present.
# Contacts: pkg/enrich/testdata/e2e/contacts.json (see contacts.example.json).
test-e2e:
	@set -a; [ -f .env.e2e ] && . ./.env.e2e; set +a; \
	ENRICH_E2E=1 go test -tags e2e -run 'E2E' -count=1 -v -timeout 30m ./pkg/enrich/

# Live end-to-end tests for `tiga signals` (sources .env.e2e if present). OpenAI: about 6 requests
# plus up to 25 classifier and 25 research-model calls. linkdapi (only with LINKDAPI_API_KEY or
# LINKDAPI_KEY): about 7 credits; without a linkdapi key those tests skip.
test-e2e-signals:
	@set -a; [ -f .env.e2e ] && . ./.env.e2e; set +a; \
	SIGNALS_E2E=1 go test -tags e2e -run 'E2E' -count=1 -v -timeout 15m ./internal/signalscmd/

clean:
	rm -rf bin/ dist/

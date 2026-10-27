# Multi-tool CLI monorepo Makefile
# Automatically discovers every CLI tool under cmd/*

TOOLS := $(notdir $(wildcard cmd/*))
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)
PLATFORMS := darwin/amd64 darwin/arm64 linux/amd64 linux/arm64

.PHONY: all build test test-e2e clean build-release $(addprefix build-,$(TOOLS)) $(addprefix build-mac-x86-,$(TOOLS))

all: build

# Build all tools in cmd/* for the host OS/architecture into bin/<tool>
build: $(addprefix build-,$(TOOLS))

$(addprefix build-,$(TOOLS)): build-%:
	@mkdir -p bin
	CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/$* ./cmd/$*

# Build any tool for x86_64 macOS: e.g. `make build-mac-x86-enrich`
$(addprefix build-mac-x86-,$(TOOLS)): build-mac-x86-%:
	@mkdir -p bin
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o bin/$*-darwin-amd64 ./cmd/$*

# Convenience alias for enrich x86 mac build
.PHONY: build-enrich-mac-x86
build-enrich-mac-x86: build-mac-x86-enrich

# Cross-compile all tools in cmd/* across darwin/amd64, darwin/arm64, linux/amd64, linux/arm64 into dist/
build-release:
	@mkdir -p dist
	@for tool in $(TOOLS); do \
		for platform in $(PLATFORMS); do \
			os=$${platform%/*}; \
			arch=$${platform#*/}; \
			out="dist/$${tool}-$${os}-$${arch}"; \
			echo "Building $${out} (version=$(VERSION))..."; \
			GOOS=$${os} GOARCH=$${arch} CGO_ENABLED=0 go build -ldflags="$(LDFLAGS)" -o "$${out}" "./cmd/$${tool}" || exit 1; \
		done; \
	done
	@cd dist && shasum -a 256 * > checksums.txt

test:
	go test -race -cover ./...

# Live end-to-end tests against real vendors (spends API credits). Sources .env.e2e if present.
# Contacts: pkg/enrich/testdata/e2e/contacts.json (see contacts.example.json).
test-e2e:
	@set -a; [ -f .env.e2e ] && . ./.env.e2e; set +a; \
	ENRICH_E2E=1 go test -tags e2e -run 'E2E' -count=1 -v -timeout 30m ./pkg/enrich/

clean:
	rm -rf bin/ dist/

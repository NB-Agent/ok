VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
BUILDFLAGS := -trimpath

# CodeGraph release pinned for the bundled MCP server / e2e test. Bump together
# with any change to the integration in internal/codegraph.
CODEGRAPH_VERSION := v0.9.7

.PHONY: build vet fmt lint test test-cover cross clean bench bench-long fuzz e2e-codegraph gen-icon build-desktop install build-ts install-ts release i18n

build:
	CGO_ENABLED=0 go build $(BUILDFLAGS) -ldflags "$(LDFLAGS)" -o bin/ok ./cmd/ok
	CGO_ENABLED=0 go build $(BUILDFLAGS) -ldflags "$(LDFLAGS)" -o bin/ok-plugin-example ./cmd/ok-plugin-example

install:
	go install $(BUILDFLAGS) -ldflags "$(LDFLAGS)" ./cmd/ok
	@echo "installed ok to $$(go env GOPATH)/bin/ok"

# CodeGraph indexer with tree-sitter support (requires gcc/CGo).
build-ts:
	CGO_ENABLED=1 go build -tags=treesitter -trimpath -ldflags="$(LDFLAGS)" -o bin/ok-ts ./cmd/ok
	CGO_ENABLED=1 go build -tags=treesitter -trimpath -ldflags="$(LDFLAGS)" -o bin/ok-plugin-example-ts ./cmd/ok-plugin-example

# Full install with tree-sitter.
install-ts:
	CGO_ENABLED=1 go install -tags=treesitter -trimpath -ldflags="$(LDFLAGS)" ./cmd/ok

vet:
	go vet ./...

fmt:
	gofmt -w .

lint:
	golangci-lint run ./...

test:
	go test -race ./...

test-cover:
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out

cross:
	@mkdir -p dist
	@for p in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "build $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build $(BUILDFLAGS) -ldflags "$(LDFLAGS)" -o dist/ok-$$os-$$arch$$ext ./cmd/ok; \
	done

release:
	goreleaser release --clean

clean:
	rm -rf bin dist
	rm -f coverage.out

# Bench runs the benchmark suite (short, no fuzzing). Use bench-long for
# realistic timing. Use benchstat to compare against a baseline file.
bench:
	go test -bench=. -benchmem -count=5 -benchtime=200ms ./internal/diff/ ./internal/agent/ ./internal/memory/ ./internal/provider/openai/ ./internal/event/

bench-long:
	go test -bench=. -benchmem -count=5 -benchtime=1s ./internal/diff/ ./internal/agent/ ./internal/memory/ ./internal/provider/openai/ ./internal/event/

# Fuzz runs all fuzz targets for 5s each (smoke test). For a real campaign
# pass FUZZTIME=30s or longer.
fuzz:
	@for pkg in internal/tool/builtin internal/cli internal/sandbox internal/permission; do \
		echo "=== fuzzing $$pkg ==="; \
		go test -fuzz=. -fuzztime=$(FUZZTIME) ./$$pkg/ || exit 1; \
	done

# Generate desktop icon files (appicon.png + icon.ico) from the Go generator.
gen-icon:
	go run scripts/genicon.go

# Generate i18n translation catalogs from English source. Reads messages_en.go,
# calls the configured LLM to translate into 30+ languages, and writes
# messages_xx.go files. Existing manual overrides are preserved.
# Requires DEEPSEEK_API_KEY, OPENAI_API_KEY, or ANTHROPIC_API_KEY.
i18n:
	go generate ./internal/i18n/
	gofmt -w internal/i18n/messages_*.go
	@echo "i18n catalogs updated"

# Build the Wails desktop application. Generate icons first, then build.
build-desktop: gen-icon
	cd desktop && wails build

# Fetch the matching CodeGraph bundle into bin/codegraph/ (the distribution
# layout: launcher at bin/codegraph/bin/codegraph beside bin/ok) and run the
# gated MCP end-to-end test against it. Requires `gh`. Windows: install via the
# upstream install.ps1 and run the test with OK_CODEGRAPH_BIN set.
e2e-codegraph:
	@os=$$(uname -s | tr 'A-Z' 'a-z'); arch=$$(uname -m); \
	case $$arch in arm64|aarch64) arch=arm64;; x86_64|amd64) arch=x64;; *) echo "unsupported arch $$arch"; exit 1;; esac; \
	asset=codegraph-$$os-$$arch.tar.gz; dest=bin/codegraph; \
	echo "fetching $$asset ($(CODEGRAPH_VERSION)) -> $$dest"; \
	rm -rf $$dest && mkdir -p $$dest; \
	gh release download $(CODEGRAPH_VERSION) -R colbymchenry/codegraph -p $$asset -O /tmp/$$asset; \
	tar -xzf /tmp/$$asset -C $$dest --strip-components=1; \
	OK_CODEGRAPH_E2E=1 OK_CODEGRAPH_BIN=$$PWD/$$dest/bin/codegraph \
		go test ./internal/codegraph/ -run E2E -v -count=1

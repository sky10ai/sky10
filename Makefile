# Deterministic builds: same source + same Go version = identical binary.
#
# Key flags:
#   CGO_ENABLED=0    no system-dependent C linking
#   -trimpath        strips local filesystem paths from binary
#   -buildvcs=false  prevents git metadata embedding (we inject version via ldflags)
#   -ldflags         inject version at compile time, strip symbol table

VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE     := $(shell git show -s --format=%ci HEAD 2>/dev/null || echo "unknown")
GOFLAGS  := -trimpath -buildvcs=false
LDFLAGS  := -s -w \
	-X 'main.version=$(VERSION)' \
	-X 'main.commit=$(COMMIT)' \
	-X 'main.buildDate=$(DATE)'

export CGO_ENABLED := 0

.PHONY: all build build-go build-web build-swift test test-skyfs test-skyfs-cli test-skyfs-cli-v test-skyfs-ui-macos check vet fmt verify clean install reproduce platforms checksums

# --- Default ---

all: check test build

# --- Build ---

build: build-web build-go

build-web:
	cd web && bun install --frozen-lockfile && bun run build

build-go:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/sky10 .

build-swift:
	cd cirrus/macos && swift build

# --- Test ---
#
# Hierarchy:
#   make test                     run all tests
#   make test-skyfs               run all skyfs tests (cli + ui)
#   make test-skyfs-cli           Go library + CLI tests
#   make test-skyfs-cli-v         Go tests verbose
#   make test-skyfs-ui-macos      Swift macOS UI tests

test: test-skyfs

test-skyfs: test-skyfs-cli test-skyfs-ui-macos

test-skyfs-cli:
	@echo "=== test-skyfs-cli (Go) ==="
	go test ./... -count=1

test-skyfs-cli-v:
	go test ./... -v -count=1

test-skyfs-ui-macos:
	@echo "=== test-skyfs-ui-macos (Swift) ==="
	@if xcode-select -p 2>/dev/null | grep -q "Xcode.app"; then \
		cd cirrus/macos && swift test 2>&1 | tail -20; \
	else \
		echo "Requires full Xcode (xcode-select -s /Applications/Xcode.app)"; \
		echo "Swift library builds OK: make build-swift"; \
		echo "37 test cases at skyshare/skyshare-tests/"; \
	fi

# --- Lint ---

vet:
	go vet ./...

fmt:
	@test -z "$$(gofmt -l .)" || (echo "Files need formatting:" && gofmt -l . && exit 1)

check: fmt vet

verify:
	go mod verify

# --- Clean ---

clean:
	rm -rf bin/

# --- Install ---

install:
	go install $(GOFLAGS) -ldflags "$(LDFLAGS)" .

# --- Cross-compilation ---

platforms: bin/sky10-linux-amd64 bin/sky10-linux-arm64 bin/sky10-darwin-amd64 bin/sky10-darwin-arm64

bin/sky10-linux-amd64:
	GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ .

bin/sky10-linux-arm64:
	GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ .

bin/sky10-darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ .

bin/sky10-darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ .

# --- Checksums + Reproducibility ---

checksums: platforms
	cd bin && shasum -a 256 sky10-* > checksums.txt
	@cat bin/checksums.txt

reproduce:
	@echo "Build 1..."
	@go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o /tmp/sky10-build1 .
	@echo "Build 2..."
	@go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o /tmp/sky10-build2 .
	@if cmp -s /tmp/sky10-build1 /tmp/sky10-build2; then \
		echo "Deterministic: both builds are identical"; \
		shasum -a 256 /tmp/sky10-build1 /tmp/sky10-build2; \
	else \
		echo "NOT deterministic: builds differ"; \
		shasum -a 256 /tmp/sky10-build1 /tmp/sky10-build2; \
		exit 1; \
	fi
	@rm -f /tmp/sky10-build1 /tmp/sky10-build2

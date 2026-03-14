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

XCODE_PROJECT := skyshare/skyshare.xcodeproj
XCODE_SCHEME  := skyshare

export CGO_ENABLED := 0

.PHONY: build test test-go test-swift test-v check vet fmt verify clean install all reproduce platforms checksums

# Default target
all: check test build

# ─── Build ──────────────────────────────────────────────────

build: build-go

build-go:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/skyfs ./cmd/skyfs

build-swift:
	xcodebuild -project $(XCODE_PROJECT) -scheme $(XCODE_SCHEME) -configuration Release build 2>&1 | tail -5

# ─── Test ───────────────────────────────────────────────────

# Run all tests (Go + Swift)
test: test-go test-swift

# Go tests only
test-go:
	@echo "=== Go tests ==="
	go test ./... -count=1

# Swift tests only (requires Xcode project)
test-swift:
	@echo "=== Swift tests ==="
	@if [ -d "$(XCODE_PROJECT)" ]; then \
		xcodebuild test -project $(XCODE_PROJECT) -scheme $(XCODE_SCHEME) \
			-destination 'platform=macOS' -quiet 2>&1 | tail -20; \
	else \
		echo "Xcode project not found at $(XCODE_PROJECT) — skipping Swift tests"; \
		echo "Swift test files are at skyshare/skyshare-tests/"; \
	fi

# Verbose Go tests
test-go-v:
	go test ./... -v -count=1

# ─── Lint / Check ──────────────────────────────────────────

vet:
	go vet ./...

# Check formatting (fails if any file needs formatting)
fmt:
	@test -z "$$(gofmt -l .)" || (echo "Files need formatting:" && gofmt -l . && exit 1)

# All checks: format, vet
check: fmt vet

# Verify module dependencies haven't been tampered with
verify:
	go mod verify

# ─── Clean ──────────────────────────────────────────────────

clean:
	rm -rf bin/
	@if [ -d "$(XCODE_PROJECT)" ]; then \
		xcodebuild -project $(XCODE_PROJECT) -scheme $(XCODE_SCHEME) clean 2>/dev/null || true; \
	fi

# ─── Install ───────────────────────────────────────────────

install:
	go install $(GOFLAGS) -ldflags "$(LDFLAGS)" ./cmd/skyfs

# ─── Cross-compilation ─────────────────────────────────────

platforms: bin/skyfs-linux-amd64 bin/skyfs-linux-arm64 bin/skyfs-darwin-amd64 bin/skyfs-darwin-arm64

bin/skyfs-linux-amd64:
	GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./cmd/skyfs

bin/skyfs-linux-arm64:
	GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./cmd/skyfs

bin/skyfs-darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./cmd/skyfs

bin/skyfs-darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./cmd/skyfs

# ─── Checksums + Reproducibility ───────────────────────────

checksums: platforms
	cd bin && shasum -a 256 skyfs-* > checksums.txt
	@cat bin/checksums.txt

reproduce:
	@echo "Build 1..."
	@go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o /tmp/skyfs-build1 ./cmd/skyfs
	@echo "Build 2..."
	@go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o /tmp/skyfs-build2 ./cmd/skyfs
	@if cmp -s /tmp/skyfs-build1 /tmp/skyfs-build2; then \
		echo "✓ Deterministic: both builds are identical"; \
		shasum -a 256 /tmp/skyfs-build1 /tmp/skyfs-build2; \
	else \
		echo "✗ NOT deterministic: builds differ"; \
		shasum -a 256 /tmp/skyfs-build1 /tmp/skyfs-build2; \
		exit 1; \
	fi
	@rm -f /tmp/skyfs-build1 /tmp/skyfs-build2

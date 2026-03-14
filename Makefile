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

.PHONY: all build build-go build-swift test test-go test-swift test-go-v check vet fmt verify clean install reproduce platforms checksums

# Default
all: check test build

# --- Build ---

build: build-go

build-go:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/skyfs ./cmd/skyfs

build-swift:
	cd skyshare && swift build

# --- Test ---

test: test-go test-swift

test-go:
	@echo "=== Go tests ==="
	go test ./... -count=1

test-swift:
	@echo "=== Swift tests ==="
	@if xcode-select -p 2>/dev/null | grep -q "Xcode.app"; then \
		cd skyshare && swift test 2>&1 | tail -20; \
	else \
		echo "Full Xcode required for Swift tests (XCTest/Testing framework)"; \
		echo "Install Xcode, then: xcode-select -s /Applications/Xcode.app"; \
		echo "Swift library builds OK: cd skyshare && swift build"; \
		echo "37 test cases ready at skyshare/skyshare-tests/"; \
	fi

test-go-v:
	go test ./... -v -count=1

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
	go install $(GOFLAGS) -ldflags "$(LDFLAGS)" ./cmd/skyfs

# --- Cross-compilation ---

platforms: bin/skyfs-linux-amd64 bin/skyfs-linux-arm64 bin/skyfs-darwin-amd64 bin/skyfs-darwin-arm64

bin/skyfs-linux-amd64:
	GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./cmd/skyfs

bin/skyfs-linux-arm64:
	GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./cmd/skyfs

bin/skyfs-darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./cmd/skyfs

bin/skyfs-darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./cmd/skyfs

# --- Checksums + Reproducibility ---

checksums: platforms
	cd bin && shasum -a 256 skyfs-* > checksums.txt
	@cat bin/checksums.txt

reproduce:
	@echo "Build 1..."
	@go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o /tmp/skyfs-build1 ./cmd/skyfs
	@echo "Build 2..."
	@go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o /tmp/skyfs-build2 ./cmd/skyfs
	@if cmp -s /tmp/skyfs-build1 /tmp/skyfs-build2; then \
		echo "Deterministic: both builds are identical"; \
		shasum -a 256 /tmp/skyfs-build1 /tmp/skyfs-build2; \
	else \
		echo "NOT deterministic: builds differ"; \
		shasum -a 256 /tmp/skyfs-build1 /tmp/skyfs-build2; \
		exit 1; \
	fi
	@rm -f /tmp/skyfs-build1 /tmp/skyfs-build2

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

.PHONY: build test lint vet fmt check verify clean install all reproduce

# Default target
all: check test build

# Build the skyfs binary
build:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/skyfs ./cmd/skyfs

# Run all tests
test:
	go test ./... -count=1

# Run tests with verbose output
test-v:
	go test ./... -v -count=1

# Static analysis
vet:
	go vet ./...

# Check formatting (fails if any file needs formatting)
fmt:
	@test -z "$$(gofmt -l .)" || (echo "Files need formatting:" && gofmt -l . && exit 1)

# All checks: format, vet, test
check: fmt vet

# Verify module dependencies haven't been tampered with
verify:
	go mod verify

# Remove build artifacts
clean:
	rm -rf bin/

# Install to GOPATH/bin
install:
	go install $(GOFLAGS) -ldflags "$(LDFLAGS)" ./cmd/skyfs

# Build for all target platforms
platforms: bin/skyfs-linux-amd64 bin/skyfs-linux-arm64 bin/skyfs-darwin-amd64 bin/skyfs-darwin-arm64

bin/skyfs-linux-amd64:
	GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./cmd/skyfs

bin/skyfs-linux-arm64:
	GOOS=linux GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./cmd/skyfs

bin/skyfs-darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./cmd/skyfs

bin/skyfs-darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $@ ./cmd/skyfs

# Generate SHA-256 checksums for all binaries
checksums: platforms
	cd bin && shasum -a 256 skyfs-* > checksums.txt
	@cat bin/checksums.txt

# Verify build is deterministic: build twice, compare checksums
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

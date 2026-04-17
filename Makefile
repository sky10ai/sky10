# Deterministic builds: same source + same Go version = identical binary.
#
# Key flags:
#   CGO_ENABLED=0    no system-dependent C linking
#   -trimpath        strips local filesystem paths from binary
#   -buildvcs=false  prevents git metadata embedding (we inject version via ldflags)
#   -ldflags         inject version at compile time, strip symbol table

VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT   := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE     := $(shell TZ=UTC git log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "unknown")
GOFLAGS  := -trimpath -buildvcs=false
LDFLAGS  := -s -w \
	-X 'main.version=$(VERSION)' \
	-X 'main.commit=$(COMMIT)' \
	-X 'main.buildDate=$(DATE)'
UNAME_S  := $(shell uname -s)
WEB_DEV_PORT ?= 5173
WEB_DEV_PATH ?= /
WEB_RPC_TARGET ?= http://localhost:9102
INSTALL_DIR ?= $(HOME)/.bin
INSTALL_BIN ?= $(INSTALL_DIR)/sky10

export CGO_ENABLED := 0

ifeq ($(UNAME_S),Darwin)
OPEN_CMD := open
MENU_LINK_RUSTFLAGS := -C link-arg=-Wl,-oso_prefix,$(CURDIR)
else ifeq ($(UNAME_S),Linux)
OPEN_CMD := xdg-open
MENU_LINK_RUSTFLAGS := -C link-arg=-Wl,--build-id=none
else
OPEN_CMD :=
MENU_LINK_RUSTFLAGS :=
endif

MENU_SOURCE_DATE_EPOCH := $(shell git log -1 --format=%ct 2>/dev/null || echo 0)
MENU_RUSTFLAGS := --remap-path-prefix=$(CURDIR)=/workspace $(MENU_LINK_RUSTFLAGS)

.PHONY: all build build-go build-web build-menu web-dev test test-skyfs test-skyfs-cli test-skyfs-cli-v test-skyfs-p2p-integration test-skyfs-daemon-integration check vet fmt verify clean install go-install reproduce reproduce-menu platforms checksums

# --- Default ---

all: check test build

# --- Build ---

build: build-web build-go

build-menu:
	cd menu/src-tauri && SOURCE_DATE_EPOCH=$(MENU_SOURCE_DATE_EPOCH) CARGO_INCREMENTAL=0 RUSTFLAGS='$(MENU_RUSTFLAGS)' cargo build --release --locked

BUN := $(shell command -v bun 2>/dev/null || echo "$(HOME)/.bun/bin/bun")

build-web:
	cd web && $(BUN) install --frozen-lockfile && $(BUN) run build

web-dev:
	cd web && $(BUN) install --frozen-lockfile
	@echo "Starting web dev UI on http://localhost:$(WEB_DEV_PORT)$(WEB_DEV_PATH)"
	@echo "Proxying /rpc and /health to $(WEB_RPC_TARGET)"
	@curl -fsS "$(WEB_RPC_TARGET)/health" >/dev/null 2>&1 || echo "Warning: no daemon responding at $(WEB_RPC_TARGET)"
	@echo "Override with: make web-dev WEB_RPC_TARGET=http://localhost:9101 WEB_DEV_PATH=/bucket"
	@if [ -n "$(OPEN_CMD)" ]; then (sleep 1; $(OPEN_CMD) "http://localhost:$(WEB_DEV_PORT)$(WEB_DEV_PATH)" >/dev/null 2>&1 || true) & fi
	cd web && SKY10_WEB_RPC_TARGET=$(WEB_RPC_TARGET) $(BUN) run dev -- --host 0.0.0.0 --port $(WEB_DEV_PORT) --strictPort

build-go:
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o bin/sky10 .

# --- Test ---
#
# Hierarchy:
#   make test                     run all tests
#   make test-skyfs               run all skyfs tests
#   make test-skyfs-cli           Go library + CLI tests
#   make test-skyfs-cli-v         Go tests verbose
#   make test-skyfs-p2p-integration     tagged FS p2p integration tests
#   make test-skyfs-daemon-integration  tagged FS daemon process integration tests

test: test-skyfs

test-skyfs: test-skyfs-cli

test-skyfs-cli:
	@echo "=== test-skyfs-cli (Go) ==="
	go test ./... -count=1

test-skyfs-cli-v:
	go test ./... -v -count=1

test-skyfs-p2p-integration:
	@echo "=== test-skyfs-p2p-integration (Go, tagged) ==="
	go test ./pkg/fs -tags=integration,skyfs_p2p -run 'TestFSP2P' -v -count=1

test-skyfs-daemon-integration:
	@echo "=== test-skyfs-daemon-integration (Go, tagged) ==="
	go test ./integration -tags=integration,skyfs_daemon -run 'TestIntegration.*FS' -v -count=1

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

install: build
	mkdir -p "$(INSTALL_DIR)"
	install -m 755 bin/sky10 "$(INSTALL_BIN)"

go-install:
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

reproduce-menu:
	@echo "Build 1..."
	@rm -rf /tmp/sky10-menu-build1 /tmp/sky10-menu-build2
	@cd menu/src-tauri && SOURCE_DATE_EPOCH=$(MENU_SOURCE_DATE_EPOCH) CARGO_INCREMENTAL=0 RUSTFLAGS='$(MENU_RUSTFLAGS)' cargo build --release --locked --target-dir /tmp/sky10-menu-build1
	@echo "Build 2..."
	@cd menu/src-tauri && SOURCE_DATE_EPOCH=$(MENU_SOURCE_DATE_EPOCH) CARGO_INCREMENTAL=0 RUSTFLAGS='$(MENU_RUSTFLAGS)' cargo build --release --locked --target-dir /tmp/sky10-menu-build2
	@if cmp -s /tmp/sky10-menu-build1/release/sky10-menu /tmp/sky10-menu-build2/release/sky10-menu; then \
		echo "Deterministic: both menu builds are identical"; \
		shasum -a 256 /tmp/sky10-menu-build1/release/sky10-menu /tmp/sky10-menu-build2/release/sky10-menu; \
	else \
		echo "NOT deterministic: menu builds differ"; \
		shasum -a 256 /tmp/sky10-menu-build1/release/sky10-menu /tmp/sky10-menu-build2/release/sky10-menu; \
		exit 1; \
	fi
	@rm -rf /tmp/sky10-menu-build1 /tmp/sky10-menu-build2

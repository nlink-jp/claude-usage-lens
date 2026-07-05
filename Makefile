BINARY   := claude-usage-lens
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -ldflags "-X main.version=$(VERSION)"
DIST_DIR := dist

.PHONY: build build-all test vet clean

## build: compile the binary into dist/ (never use `go build` directly)
build:
	@mkdir -p $(DIST_DIR)
	go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY) .

## build-all: cross-compile all platforms, CGO-free (pure-Go SQLite = no Podman needed).
## Windows/Linux are experimental — source paths are unverified on real hardware (RFP §7).
build-all:
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-darwin-arm64      .
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-darwin-amd64      .
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-linux-arm64       .
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-linux-amd64       .
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(DIST_DIR)/$(BINARY)-windows-amd64.exe .

## test: run all tests
test:
	go test ./...

## vet: static checks across every target OS (covers build-tagged platform files)
vet:
	go vet ./...
	GOOS=windows go vet ./...
	GOOS=linux   go vet ./...

## clean: remove build artifacts
clean:
	rm -rf $(DIST_DIR)

# NOTE: Developer ID signing / notarization + release `package` target are added
# in the release phase (Phase 3), per nlink-jp CONVENTIONS.md §Code Signing.

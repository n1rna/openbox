BIN := openbox
PKG := ./cmd/openbox
DIST := dist

# Version stamped into the binary. Defaults to the latest git tag (minus the
# leading v) plus a -dev suffix when the tree has moved past the tag; override
# with `make build VERSION=1.2.3`. CI passes the release tag here.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//')
LDFLAGS := -s -w -X main.version=$(VERSION)

# Platforms we ship prebuilt binaries for (GOOS/GOARCH).
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build test vet fmt run-control clean dist

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) $(PKG)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

run-control: build
	./$(BIN) control

# Cross-compiled static binaries + tarballs + checksums for every platform,
# matching exactly what the release workflow produces.
dist:
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		out=$(BIN)-$$os-$$arch; \
		echo "building $$out ($(VERSION))"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -ldflags "$(LDFLAGS)" -o $(DIST)/$$out/$(BIN) $(PKG); \
		tar -C $(DIST)/$$out -czf $(DIST)/$$out.tar.gz $(BIN); \
		( cd $(DIST) && shasum -a 256 $$out.tar.gz > $$out.tar.gz.sha256 ); \
		rm -rf $(DIST)/$$out; \
	done
	@ls -la $(DIST)

clean:
	rm -f $(BIN)
	rm -rf $(DIST)

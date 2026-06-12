BIN := openbox
PKG := ./cmd/openbox
DIST := dist

.PHONY: build test vet fmt run-control clean dist

build:
	go build -o $(BIN) $(PKG)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

run-control: build
	./$(BIN) control

# Cross-compiled static binaries for the platforms nodes commonly run.
dist:
	mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -o $(DIST)/$(BIN)-linux-amd64  $(PKG)
	CGO_ENABLED=0 GOOS=linux  GOARCH=arm64 go build -o $(DIST)/$(BIN)-linux-arm64  $(PKG)
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o $(DIST)/$(BIN)-darwin-arm64 $(PKG)
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o $(DIST)/$(BIN)-darwin-amd64 $(PKG)

clean:
	rm -f $(BIN)
	rm -rf $(DIST)

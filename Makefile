BIN     := dedupe
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build test install clean fmt vet

all: vet test build

build: ## build ./bin/dedupe
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BIN) .

test: ## run the test suite
	go test -race ./...

install: ## install into $(go env GOPATH)/bin
	go install -trimpath -ldflags "$(LDFLAGS)" .

vet:
	go vet ./...

fmt:
	gofmt -s -w .

clean:
	rm -rf bin

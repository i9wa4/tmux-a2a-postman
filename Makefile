BIN := postman
CURRENT_REVISION := $(shell git rev-parse --short HEAD)
BUILD_LDFLAGS := "-s -w -X main.revision=$(CURRENT_REVISION)"

.PHONY: build
build:
	mkdir -p bin
	go build -ldflags=$(BUILD_LDFLAGS) -o bin/$(BIN) .

.PHONY: test
test:
	go test -v -race ./...

.PHONY: fmt
fmt:
	go fmt ./...
	golangci-lint run --fix ./...

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: clean
clean:
	go clean
	rm -rf bin/

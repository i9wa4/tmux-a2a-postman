BIN := tmux-a2a-postman
CURRENT_REVISION := $(shell git rev-parse --short HEAD)
BUILD_LDFLAGS := "-s -w -X main.revision=$(CURRENT_REVISION)"

.PHONY: build
build:
	go build -ldflags=$(BUILD_LDFLAGS) -o $(BIN) ./cmd/postman

.PHONY: test
test:
	go test -v -race ./...

.PHONY: test-coverage
test-coverage:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

.PHONY: fmt
fmt:
	go fmt ./...
	golangci-lint run --fix ./...

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: security
security:
	go vet ./...
	govulncheck ./...

.PHONY: clean
clean:
	go clean
	rm -f $(BIN)

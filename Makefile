BIN := tmux-a2a-postman

.PHONY: build
build:
	go build -o $(BIN) ./

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

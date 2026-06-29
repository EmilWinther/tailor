.PHONY: fmt lint test build

fmt:
	gofmt -w -s ./...

lint:
	golangci-lint run ./...

test:
	go test ./...

build:
	go build -o tailor ./cmd/tailor

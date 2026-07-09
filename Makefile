.PHONY: fmt lint test build run up down

fmt:
	gofmt -w -s $$(go list -f '{{.Dir}}' ./...)

lint:
	golangci-lint run ./...

test:
	go test ./...

build:
	go build -o bin/api ./cmd/api

run: build
	./bin/api

up:
	docker compose up -d --wait

down:
	docker compose down

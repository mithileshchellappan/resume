.PHONY: build run test fmt tidy

build:
	go build -o bin/resume ./cmd/resume

run:
	go run ./cmd/resume

test:
	go test ./...

fmt:
	go fmt ./...

tidy:
	go mod tidy

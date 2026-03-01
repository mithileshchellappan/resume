BINARY_NAME := resume
PKG := ./cmd/resume
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "0.1.0-dev")
LDFLAGS := -s -w -X github.com/mithileshchellappan/resume/internal/buildinfo.Version=$(VERSION)

.PHONY: all build run test vet fmt format-check tidy clean build-all

all: build

build:
	mkdir -p bin
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY_NAME) $(PKG)

run:
	./bin/$(BINARY_NAME) --help

test:
	go test ./...

vet:
	go vet ./...

fmt:
	go fmt ./...

format-check:
	@unformatted="$$(find . -name '*.go' -not -path './vendor/*' -print0 | xargs -0 gofmt -l)"; \
	if [ -n "$$unformatted" ]; then \
		echo "The following files are not gofmt-formatted:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

tidy:
	go mod tidy

build-all:
	mkdir -p dist
	GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)_$(VERSION)_macOS_amd64 $(PKG)
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)_$(VERSION)_macOS_arm64 $(PKG)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)_$(VERSION)_linux_amd64 $(PKG)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)_$(VERSION)_linux_arm64 $(PKG)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)_$(VERSION)_windows_amd64.exe $(PKG)
	cd dist && sha256sum * > $(BINARY_NAME)_$(VERSION)_checksums.txt

clean:
	rm -rf bin dist

.PHONY: build test lint vet fmt clean

VERSION ?= dev

build:
	CGO_ENABLED=1 go build -ldflags "-X main.version=$(VERSION)" -o opencode-msb ./cmd/opencode-msb

test:
	CGO_ENABLED=1 go test ./...

lint:
	golangci-lint run ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -f opencode-msb

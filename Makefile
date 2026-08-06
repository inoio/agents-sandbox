.PHONY: build test lint fmt clean completion user-install check all

VERSION ?= dev

build:
	CGO_ENABLED=1 go build -ldflags "-X main.version=$(VERSION)" -o opencode-msb ./cmd/opencode-msb

test:
	CGO_ENABLED=1 go test ./...

lint:
	golangci-lint run ./...

fmt:
	golangci-lint fmt ./...

run:
	go run ./cmd/opencode-msb

check: fmt lint test

all: fmt lint test build

clean:
	rm -f opencode-msb

completion:
	mkdir -p ~/.local/share/bash-completion/completions
	go run ./cmd/opencode-msb completion bash > ~/.local/share/bash-completion/completions/opencode-msb

user-install: build
	mkdir -p ~/.local/bin
	cp opencode-msb ~/.local/bin

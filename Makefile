.PHONY: build test lint vet fmt clean completion

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

completion:
	mkdir -p ~/.local/share/bash-completion/completions
	go run ./cmd/opencode-msb completion bash > ~/.local/share/bash-completion/completions/opencode-msb

.PHONY: build test lint vet fmt clean

build:
	CGO_ENABLED=1 go build -o opencode-msb ./cmd/opencode-msb

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

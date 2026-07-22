.PHONY: build test lint vet fmt clean

build:
	go build -o opencode-msb ./cmd/opencode-msb

test:
	go test ./...

lint:
	golangci-lint run ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -f opencode-msb

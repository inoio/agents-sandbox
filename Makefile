.PHONY: build build-release build-release-all test lint fmt clean completion user-install check all

VERSION ?= dev

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
ZIG_TARGET ?=

ARTIFACT = opencode-msb-$(GOOS)-$(GOARCH)

RELEASE_TARGETS = \
	linux/amd64/x86_64-linux-gnu \
	linux/arm64/aarch64-linux-gnu \
	darwin/arm64/aarch64-macos.11.0-none

build:
	CGO_ENABLED=1 go build -ldflags "-X main.version=$(VERSION)" -o opencode-msb ./cmd/opencode-msb

build-release: export CC=zig cc -target $(ZIG_TARGET)
build-release: export CXX=zig c++ -target $(ZIG_TARGET)

build-release:
	@test -n "$(ZIG_TARGET)" || { echo "ZIG_TARGET is required (e.g. x86_64-linux-gnu)"; exit 1; }
	@echo "building $(ARTIFACT) (zig target: $(ZIG_TARGET))"
ifeq ($(GOOS),darwin)
	CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) \
	    CGO_CFLAGS="-isystem $(shell dirname $(shell which zig))/lib/libc/include/any-darwin-any" \
	    CGO_LDFLAGS="-F $(shell dirname $(shell which zig))/lib/libc/darwin/System/Library/Frameworks -L $(shell dirname $(shell which zig))/lib/libc/darwin/usr/lib -Wl,-undefined,dynamic_lookup" \
	    go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$(VERSION)" -o $(ARTIFACT) ./cmd/opencode-msb
else
	CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) \
	    go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$(VERSION)" -o $(ARTIFACT) ./cmd/opencode-msb
endif

build-release-all: export VERSION=$(VERSION)
build-release-all:
	@for t in $(RELEASE_TARGETS); do \
	    goos=$${t%/*/*}; rest=$${t#*/}; goarch=$${rest%/*}; zig=$${t##*/}; \
	    $(MAKE) build-release GOOS=$$goos GOARCH=$$goarch ZIG_TARGET=$$zig; \
	done

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
	cp opencode-msb ~/.local/bin/opencode-msb.tmp
	mv -f ~/.local/bin/opencode-msb.tmp ~/.local/bin/opencode-msb

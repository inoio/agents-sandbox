.PHONY: build build-release build-release-all test coverage lint fmt clean completion user-install check all upgrade-deps docs-diagrams

VERSION ?= dev

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
ZIG_TARGET ?=
PLANTUML ?= plantuml

ARTIFACT = opencode-sandbox-$(GOOS)-$(GOARCH)

RELEASE_TARGETS = \
	linux/amd64/x86_64-linux-gnu \
	linux/arm64/aarch64-linux-gnu \
	darwin/arm64/aarch64-macos.11.0-none

build:
	CGO_ENABLED=1 go build -ldflags "-X main.version=$(VERSION)" -o opencode-sandbox ./cmd/opencode-sandbox

build-release: export CC=zig cc -target $(ZIG_TARGET)
build-release: export CXX=zig c++ -target $(ZIG_TARGET)

build-release:
	@test -n "$(ZIG_TARGET)" || { echo "ZIG_TARGET is required (e.g. x86_64-linux-gnu)"; exit 1; }
	@echo "building $(ARTIFACT) (zig target: $(ZIG_TARGET))"
ifeq ($(GOOS),darwin)
	CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) \
	    CGO_CFLAGS="-isystem $(shell dirname $(shell which zig))/lib/libc/include/any-darwin-any" \
	    CGO_LDFLAGS="-F $(shell dirname $(shell which zig))/lib/libc/darwin/System/Library/Frameworks -L $(shell dirname $(shell which zig))/lib/libc/darwin/usr/lib -Wl,-undefined,dynamic_lookup" \
	    go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$(VERSION)" -o $(ARTIFACT) ./cmd/opencode-sandbox
else
	CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) \
	    go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=$(VERSION)" -o $(ARTIFACT) ./cmd/opencode-sandbox
endif

build-release-all: export VERSION=$(VERSION)
build-release-all:
	@for t in $(RELEASE_TARGETS); do \
	    goos=$${t%/*/*}; rest=$${t#*/}; goarch=$${rest%/*}; zig=$${t##*/}; \
	    $(MAKE) build-release GOOS=$$goos GOARCH=$$goarch ZIG_TARGET=$$zig; \
	done

test:
	CGO_ENABLED=1 go test ./...

coverage:
	CGO_ENABLED=1 go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run ./...

fmt:
	golangci-lint fmt ./...

validate-fmt:
	golangci-lint fmt -d ./...

run:
	go run ./cmd/opencode-sandbox

check: fmt lint test

all: fmt lint test build

clean:
	rm -f opencode-sandbox

completion:
	mkdir -p ~/.local/share/bash-completion/completions
	go run ./cmd/opencode-sandbox completion bash > ~/.local/share/bash-completion/completions/opencode-sandbox

# Render PlantUML diagrams in docs/diagrams/ to SVG (local preview; CI re-renders on release).
# Excludes the vendored C4-PlantUML library files (C4*.puml), which are not standalone diagrams.
docs-diagrams:
	cd docs/diagrams && for f in *.puml; do case "$$f" in C4*) ;; *) $(PLANTUML) -DRELATIVE_INCLUDE -tsvg -o . "$$f" || exit 1;; esac; done

user-install: build
	mkdir -p ~/.local/bin
	cp opencode-sandbox ~/.local/bin/opencode-sandbox.tmp
	mv -f ~/.local/bin/opencode-sandbox.tmp ~/.local/bin/opencode-sandbox

upgrade-deps:
	go get -u ./...
	go mod tidy

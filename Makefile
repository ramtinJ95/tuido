BIN     := tuido
PREFIX  ?= $(HOME)/.local
GO      ?= go
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: all build install uninstall test fuzz vet fmt check clean

all: build

build:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/tuido

install: build
	install -d $(PREFIX)/bin $(PREFIX)/share/zsh/site-functions
	install -m 0755 $(BIN) $(PREFIX)/bin/$(BIN)
	install -m 0644 completions/_tuido $(PREFIX)/share/zsh/site-functions/_tuido

uninstall:
	rm -f $(PREFIX)/bin/$(BIN) $(PREFIX)/share/zsh/site-functions/_tuido

test:
	$(GO) test ./...

# The single highest-value test in the project: a parser bug does not crash, it
# quietly rewrites a line and git dutifully commits it.
fuzz:
	$(GO) test ./internal/task -run FuzzParse -fuzz FuzzParse -fuzztime=60s

vet:
	$(GO) vet ./...

fmt:
	gofmt -l -w cmd internal

check: vet test
	@test -z "$$(gofmt -l cmd internal)" || { echo 'gofmt needed:'; gofmt -l cmd internal; exit 1; }

clean:
	rm -f $(BIN)
	$(GO) clean -testcache

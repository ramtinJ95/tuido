BIN     := tuido
PREFIX  ?= $(HOME)/.local
GO      ?= go
VERSION := $(shell test -z "$$(git status --porcelain 2>/dev/null)" && \
	git describe --tags --exact-match 2>/dev/null || echo dev)
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

# --- releasing ---------------------------------------------------------------
# No CI: cutting a release is one command on your machine. `tuido upgrade`
# downloads these assets and verifies them against checksums.txt, so the names
# here must match selfupdate.AssetName.
PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64
DIST      := dist

.PHONY: dist release

dist:
	rm -rf $(DIST) && mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; \
	  echo "  building tuido_$${os}_$${arch}"; \
	  GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags '$(LDFLAGS)' \
	    -o $(DIST)/tuido_$${os}_$${arch} ./cmd/tuido || exit 1; \
	done
	cd $(DIST) && shasum -a 256 tuido_* > checksums.txt
	@echo "✓ $(DIST)/ built at version $(VERSION)"

# make release TAG=v0.2.0
release:
	@test -n "$(TAG)" || { echo "usage: make release TAG=v0.2.0"; exit 1; }
	@printf '%s\n' "$(TAG)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$$' || \
	  { echo "TAG must be a semantic version such as v0.2.0"; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo "working tree is dirty"; exit 1; }
	@test "$$(git branch --show-current)" = main || { echo "release must run from main"; exit 1; }
	git fetch --prune origin main
	@git merge-base --is-ancestor origin/main HEAD || \
	  { echo "local main has diverged from origin/main"; exit 1; }
	$(MAKE) dist VERSION=$(TAG)
	git tag -a $(TAG) -m "$(TAG)"
	git push --atomic origin main $(TAG)
	gh release create $(TAG) $(DIST)/* --title $(TAG) --generate-notes --verify-tag
	@echo "✓ released $(TAG) — users get it via 'tuido upgrade'"

clean:
	rm -f $(BIN)
	rm -rf $(DIST)
	$(GO) clean -testcache

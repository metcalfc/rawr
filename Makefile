GO ?= go
VERSION ?= dev
COMMIT ?= unknown
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
APPS := ghrawr filerawr exerawr rawr
COMPAT_APPS := ghrex filerex exerex
LDFLAGS ?= -X github.com/metcalfc/rawr/internal/buildinfo.Version=$(VERSION) -X github.com/metcalfc/rawr/internal/buildinfo.Commit=$(COMMIT)

.PHONY: all build build-compat test vet sdk-smoke install install-compat release clean
all: build

build:
	@mkdir -p build/bin
	@set -eu; for app in $(APPS); do \
		CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -ldflags "$(LDFLAGS)" -o "build/bin/$$app" "./cmd/$$app"; \
	done

# Explicit local aliases; release bundles contain only the current names.
build-compat: build
	@set -eu; for app in $(COMPAT_APPS); do \
		CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -ldflags "$(LDFLAGS)" -o "build/bin/$$app" "./cmd/$$app"; \
	done

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

sdk-smoke:
	$(GO) run ./scripts/sdk-smoke -go "$(GO)"

install: build
	@mkdir -p "$(BINDIR)"
	@set -eu; for app in $(APPS); do install -m 0755 "build/bin/$$app" "$(BINDIR)/$$app"; done

install-compat: install build-compat
	@set -eu; for app in $(COMPAT_APPS); do install -m 0755 "build/bin/$$app" "$(BINDIR)/$$app"; done

# Override TARGETS for a smaller matrix, e.g. TARGETS=darwin/arm64.
# Windows targets are build artifacts only; Rex integration is unverified there.
release:
	$(GO) run ./scripts/release.go -version "$(VERSION)" -commit "$(COMMIT)" $(if $(TARGETS),-targets "$(TARGETS)",)

clean:
	rm -rf build dist

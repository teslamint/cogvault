BINARY     = cogvault
INSTALL_DIR = $(HOME)/bin
CODESIGN_IDENTITY   ?= -
CODESIGN_IDENTIFIER ?= dev.tmint.cogvault
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null)
LDFLAGS = -X github.com/teslamint/cogvault/internal/version.injected=$(VERSION)

.PHONY: build install test lint clean

build:
	@set -eu; \
		tmp=".$(BINARY).build"; \
		trap 'rm -f "$$tmp"' EXIT HUP INT TERM; \
		go build -ldflags "$(LDFLAGS)" -o "$$tmp" ./cmd/cogvault/; \
		echo "codesign: identity=$(CODESIGN_IDENTITY) identifier=$(CODESIGN_IDENTIFIER)"; \
		codesign --force --sign "$(CODESIGN_IDENTITY)" --identifier "$(CODESIGN_IDENTIFIER)" "$$tmp"; \
		codesign --verify --strict --verbose=4 "$$tmp"; \
		mv -f "$$tmp" $(BINARY); \
		trap - EXIT HUP INT TERM

install: build
	mkdir -p $(INSTALL_DIR)
	@set -eu; \
		tmp="$(INSTALL_DIR)/.$(BINARY).tmp"; \
		trap 'rm -f "$$tmp"' EXIT HUP INT TERM; \
		cp $(BINARY) "$$tmp"; \
		echo "codesign: identity=$(CODESIGN_IDENTITY) identifier=$(CODESIGN_IDENTIFIER)"; \
		codesign --force --sign "$(CODESIGN_IDENTITY)" --identifier "$(CODESIGN_IDENTIFIER)" "$$tmp"; \
		codesign --verify --strict --verbose=4 "$$tmp"; \
		mv -f "$$tmp" $(INSTALL_DIR)/$(BINARY); \
		trap - EXIT HUP INT TERM

test:
	sh scripts/make_build_test.sh
	sh scripts/make_install_test.sh
	sh scripts/install-signed_test.sh
	go test -race ./...

lint:
	sh scripts/lint.sh

clean:
	rm -f $(BINARY)

BINARY     = cogvault
INSTALL_DIR = $(HOME)/bin
CODESIGN_IDENTITY   ?= -
CODESIGN_IDENTIFIER ?= dev.tmint.cogvault

.PHONY: build install test clean

build:
	@set -eu; \
		tmp=".$(BINARY).build"; \
		trap 'rm -f "$$tmp"' EXIT HUP INT TERM; \
		go build -o "$$tmp" ./cmd/cogvault/; \
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

clean:
	rm -f $(BINARY)

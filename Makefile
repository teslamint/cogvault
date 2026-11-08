BINARY     = cogvault
INSTALL_DIR = $(HOME)/bin
CODESIGN_IDENTITY   ?= -
CODESIGN_IDENTIFIER ?= dev.tmint.cogvault
CODESIGN_REQUIREMENT ?=
export CODESIGN_IDENTITY
export CODESIGN_IDENTIFIER
export CODESIGN_REQUIREMENT
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null)
LDFLAGS = -X github.com/teslamint/cogvault/internal/version.injected=$(VERSION)

.PHONY: build install test lint clean

build:
	@set -eu; \
		tmp=".$(BINARY).build.$$$$"; \
		trap 'rm -f "$$tmp"' EXIT HUP INT TERM; \
		go build -ldflags "$(LDFLAGS)" -o "$$tmp" ./cmd/cogvault/; \
		echo "codesign: identity=$(CODESIGN_IDENTITY) identifier=$(CODESIGN_IDENTIFIER)"; \
		codesign --force --sign "$(CODESIGN_IDENTITY)" --identifier "$(CODESIGN_IDENTIFIER)" "$$tmp"; \
		codesign --verify --strict --verbose=4 "$$tmp"; \
		mv -f "$$tmp" $(BINARY); \
		trap - EXIT HUP INT TERM

install:
	mkdir -p "$(INSTALL_DIR)"
	@sh scripts/install-lock.sh "$(INSTALL_DIR)" \
		/bin/sh -c 'set -eu; \
			target_dir="$$1"; \
			binary="$$2"; \
			tmp="$$target_dir/.$$binary.tmp.$$$$"; \
			trap "rm -f \"\$$tmp\"" EXIT; \
			trap "exit 1" HUP INT TERM; \
			go build -ldflags "$(LDFLAGS)" -o "$$tmp" ./cmd/cogvault/; \
			echo "codesign: identity=$$CODESIGN_IDENTITY identifier=$$CODESIGN_IDENTIFIER"; \
			codesign --force --sign "$$CODESIGN_IDENTITY" --identifier "$$CODESIGN_IDENTIFIER" "$$tmp"; \
			codesign --verify --strict --verbose=4 "$$tmp"; \
			if [ -n "$$CODESIGN_REQUIREMENT" ]; then \
				codesign --verify --strict -R "$$CODESIGN_REQUIREMENT" "$$tmp"; \
			fi; \
			mv -f "$$tmp" "$$target_dir/$$binary"; \
			trap - EXIT HUP INT TERM; \
			rm -f "$$tmp"' _ "$(INSTALL_DIR)" "$(BINARY)"

test:
	sh scripts/make_build_test.sh
	sh scripts/make_install_test.sh
	sh scripts/install-signed_test.sh
	go test -race ./...

lint:
	sh scripts/lint.sh

clean:
	rm -f $(BINARY)

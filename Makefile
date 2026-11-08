BINARY     = cogvault
INSTALL_DIR = $(HOME)/bin
CODESIGN_IDENTITY   ?= -
CODESIGN_IDENTIFIER ?= dev.tmint.cogvault
CODESIGN_REQUIREMENT ?=
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
	mkdir -p $(INSTALL_DIR)
	@set -eu; \
		lock_dir="$(INSTALL_DIR)/.$(BINARY).install.lock.d"; \
		acquired_lock=0; \
		if [ "$${COGVAULT_INSTALL_LOCK_HELD:-0}" != "1" ]; then \
			attempts=0; \
			while ! mkdir "$$lock_dir" 2>/dev/null; do \
				if [ -f "$$lock_dir/pid" ]; then \
					holder_pid=$$(cat "$$lock_dir/pid" 2>/dev/null || true); \
					if [ -n "$$holder_pid" ] && ! kill -0 "$$holder_pid" 2>/dev/null; then \
						rm -rf "$$lock_dir" 2>/dev/null || true; \
						continue; \
					fi; \
				fi; \
				attempts=$$((attempts + 1)); \
				if [ "$$attempts" -ge 300 ]; then \
					printf 'timed out waiting for installation lock: %s\n' "$$lock_dir" >&2; \
					exit 1; \
				fi; \
				sleep 0.05 2>/dev/null || sleep 1; \
			done; \
			echo "$$$$" > "$$lock_dir/pid"; \
			acquired_lock=1; \
		fi; \
		tmp="$(INSTALL_DIR)/.$(BINARY).tmp.$$$$"; \
		trap 'rm -f "$$tmp"; if [ "$$acquired_lock" = "1" ]; then rm -rf "$$lock_dir"; fi' EXIT HUP INT TERM; \
		go build -ldflags "$(LDFLAGS)" -o "$$tmp" ./cmd/cogvault/; \
		echo "codesign: identity=$(CODESIGN_IDENTITY) identifier=$(CODESIGN_IDENTIFIER)"; \
		codesign --force --sign "$(CODESIGN_IDENTITY)" --identifier "$(CODESIGN_IDENTIFIER)" "$$tmp"; \
		codesign --verify --strict --verbose=4 "$$tmp"; \
		if [ -n "$$CODESIGN_REQUIREMENT" ]; then \
			codesign --verify --strict -R "$$CODESIGN_REQUIREMENT" "$$tmp"; \
		fi; \
		mv -f "$$tmp" "$(INSTALL_DIR)/$(BINARY)"; \
		rm -f "$$tmp"; \
		if [ "$$acquired_lock" = "1" ]; then \
			rm -rf "$$lock_dir"; \
		fi; \
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

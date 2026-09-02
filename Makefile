BINARY     = cogvault
INSTALL_DIR = $(HOME)/bin
CODESIGN_IDENTITY   ?= -
CODESIGN_IDENTIFIER ?= dev.tmint.cogvault

.PHONY: build install test clean

build:
	go build -o $(BINARY) ./cmd/cogvault/
	@echo "codesign: identity=$(CODESIGN_IDENTITY) identifier=$(CODESIGN_IDENTIFIER)"
	codesign --force --sign "$(CODESIGN_IDENTITY)" --identifier "$(CODESIGN_IDENTIFIER)" $(BINARY)

install: build
	mkdir -p $(INSTALL_DIR)
	cp $(BINARY) $(INSTALL_DIR)/$(BINARY)
	@echo "codesign: identity=$(CODESIGN_IDENTITY) identifier=$(CODESIGN_IDENTIFIER)"
	codesign --force --sign "$(CODESIGN_IDENTITY)" --identifier "$(CODESIGN_IDENTIFIER)" $(INSTALL_DIR)/$(BINARY)

test:
	go test -race ./...

clean:
	rm -f $(BINARY)

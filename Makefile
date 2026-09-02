BINARY     = cogvault
INSTALL_DIR = $(HOME)/bin

.PHONY: build install test clean

build:
	go build -o $(BINARY) ./cmd/cogvault/
	codesign --force --sign - $(BINARY)

install: build
	mkdir -p $(INSTALL_DIR)
	cp $(BINARY) $(INSTALL_DIR)/$(BINARY)
	codesign --force --sign - $(INSTALL_DIR)/$(BINARY)

test:
	go test -race ./...

clean:
	rm -f $(BINARY)

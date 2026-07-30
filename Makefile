BINARY  := peel
PREFIX  ?= /opt/homebrew
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/ziadalzarka/peel/internal/cli.Version=$(VERSION)
SOURCES := $(shell find . -name '*.go' -not -path './.git/*') go.mod go.sum

.PHONY: build install uninstall test clean

$(BINARY): $(SOURCES)
	go build -ldflags "$(LDFLAGS)" -o $@ .

build: $(BINARY)

install: $(BINARY)
	install -m 0755 $(BINARY) $(PREFIX)/bin/$(BINARY)

uninstall:
	rm -f $(PREFIX)/bin/$(BINARY)

test:
	go test ./...

clean:
	rm -f $(BINARY)

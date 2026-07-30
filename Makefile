BINARY  := peel
PREFIX  ?= /opt/homebrew
SKILL   := peel-review
SKILLS  ?= $(HOME)/.claude/skills
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/ziadalzarka/peel/internal/cli.Version=$(VERSION)
SOURCES := $(shell find . -name '*.go' -not -path './.git/*') go.mod go.sum

.PHONY: build install install-skill uninstall test clean

$(BINARY): $(SOURCES)
	go build -ldflags "$(LDFLAGS)" -o $@ .

build: $(BINARY)

install: $(BINARY) install-skill
	install -m 0755 $(BINARY) $(PREFIX)/bin/$(BINARY)

install-skill:
	mkdir -p $(SKILLS)
	ln -sfn $(CURDIR)/skills/$(SKILL) $(SKILLS)/$(SKILL)

uninstall:
	rm -f $(PREFIX)/bin/$(BINARY)
	rm -f $(SKILLS)/$(SKILL)

test:
	go test ./...

clean:
	rm -f $(BINARY)

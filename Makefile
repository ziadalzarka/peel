BINARY  := peel
PREFIX  ?= /opt/homebrew
SKILL   := peel-review
SKILLS  ?= $(HOME)/.claude/skills
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/ziadalzarka/peel/internal/cli.Version=$(VERSION)
SOURCES := $(shell find . -name '*.go' -not -path './.git/*') go.mod go.sum

DIST      := dist
PLATFORMS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64
SHA256    := $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo shasum -a 256)

.PHONY: build install install-skill uninstall test dist clean

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

dist: $(SOURCES)
	rm -rf $(DIST)
	mkdir -p $(DIST)
	for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		stage=$(DIST)/stage; \
		rm -rf $$stage && mkdir -p $$stage/skills || exit 1; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build -trimpath -ldflags "$(LDFLAGS)" -o $$stage/$(BINARY) . || exit 1; \
		cp -R skills/$(SKILL) $$stage/skills/$(SKILL) || exit 1; \
		tar -czf $(DIST)/$(BINARY)_$(VERSION)_$${os}_$${arch}.tar.gz -C $$stage . || exit 1; \
		rm -rf $$stage; \
	done
	cd $(DIST) && $(SHA256) *.tar.gz > checksums.txt

clean:
	rm -f $(BINARY)
	rm -rf $(DIST)

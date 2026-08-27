# niffler-tui — Bubble Tea terminal client for Niffler.
#
# This checkout is expected next to Niffler so the go.mod replacement
# (niffler.dev/sdk => ../niffler/sdk/go) resolves. The Niffler builder does
# not use this Makefile; it creates an isolated module with its own SDK
# replacement (see README).

BIN      := bin/niffler-tui
GO       ?= go
GOFLAGS  ?=

.PHONY: all build test vet run clean

all: build

build:
	@mkdir -p bin
	$(GO) build $(GOFLAGS) -o $(BIN) ./tui

test:
	$(GO) test $(GOFLAGS) ./tui

vet:
	$(GO) vet $(GOFLAGS) ./tui

run: build
	./$(BIN)

clean:
	rm -f $(BIN)

BIN := bin/mm
PKG := ./cmd/mm
GO  ?= go

.PHONY: all build run clean tidy vet fmt install

all: build

build: $(BIN)

$(BIN): $(shell find . -name '*.go' -not -path './bin/*' 2>/dev/null) go.mod
	@mkdir -p bin
	$(GO) build -o $(BIN) $(PKG)

run: build
	./$(BIN) $(ARGS)

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

install: build
	install -Dm755 $(BIN) $(HOME)/.local/bin/mm

clean:
	rm -rf bin

BIN     := bin/mm
WBIN    := bin/cc-waybar
PKG     := ./cmd/mm
WPKG    := ./cmd/cc-waybar
GO      ?= go

# Install locations. Override e.g. `make install PREFIX=/usr/local`.
PREFIX  ?= $(HOME)/.local
BINDIR  := $(PREFIX)/bin
UNITDIR ?= $(HOME)/.config/systemd/user
SERVICE := cc-usage-refresh.service
TIMER   := cc-usage-refresh.timer

SRC := $(shell find . -name '*.go' -not -path './bin/*' 2>/dev/null) go.mod

.PHONY: all build build-widget run vet fmt tidy clean \
        install install-widget uninstall-widget \
        install-service uninstall-service service-status \
        install-all uninstall-all help

all: build build-widget

## build: compile the mm GUI binary
build: $(BIN)
$(BIN): $(SRC)
	@mkdir -p bin
	$(GO) build -o $(BIN) $(PKG)

## build-widget: compile the cc-waybar widget binary
build-widget: $(WBIN)
$(WBIN): $(SRC)
	@mkdir -p bin
	$(GO) build -o $(WBIN) $(WPKG)

run: build
	./$(BIN) $(ARGS)

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

## install: install the mm GUI binary to $(BINDIR)
install: build
	install -Dm755 $(BIN) $(BINDIR)/mm

## install-widget: install the cc-waybar binary (the Waybar module runs it)
install-widget: build-widget
	install -Dm755 $(WBIN) $(BINDIR)/cc-waybar
	@echo "==> installed $(BINDIR)/cc-waybar"
	@echo "    add the module + CSS from dist/waybar/README.md to ~/.config/waybar/,"
	@echo "    then reload: killall -SIGUSR2 waybar"
	@echo "    (left-click opens the mm GUI — 'make install' to get it)"

## uninstall-widget: remove the cc-waybar binary (Waybar config left untouched)
uninstall-widget:
	rm -f $(BINDIR)/cc-waybar
	@echo "==> removed $(BINDIR)/cc-waybar (edit ~/.config/waybar/config to drop the module)"

## install-service: install + enable the systemd --user poller (5-min timer)
install-service: install-widget
	install -Dm644 dist/systemd/$(SERVICE) $(UNITDIR)/$(SERVICE)
	install -Dm644 dist/systemd/$(TIMER)   $(UNITDIR)/$(TIMER)
	systemctl --user daemon-reload
	systemctl --user enable --now $(TIMER)
	@echo "==> poller enabled; schedule: systemctl --user list-timers $(TIMER)"

## uninstall-service: disable + remove the poller units
uninstall-service:
	-systemctl --user disable --now $(TIMER)
	rm -f $(UNITDIR)/$(TIMER) $(UNITDIR)/$(SERVICE)
	systemctl --user daemon-reload
	@echo "==> poller removed"

## service-status: show the timer schedule and last service run
service-status:
	-systemctl --user list-timers $(TIMER) --no-pager
	-systemctl --user status $(SERVICE) --no-pager

## install-all: mm GUI + widget + poller
install-all: install install-service

## uninstall-all: poller + widget + mm GUI
uninstall-all: uninstall-service uninstall-widget
	rm -f $(BINDIR)/mm
	@echo "==> removed $(BINDIR)/mm"

clean:
	rm -rf bin

## help: list the documented targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /'

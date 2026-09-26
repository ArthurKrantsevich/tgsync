.PHONY: build test it vet install uninstall status logs check

EXE := $(shell go env GOEXE)
UNAME := $(shell uname -s 2>/dev/null)
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/tgsync$(EXE) ./cmd/tgsync

test:
	go test -race ./...

# Проверки на реальном claude CLI: нужен авторизованный `claude` в PATH.
it:
	go test -tags integration -run Integration -v -timeout 30m ./internal/agent/

vet:
	go vet ./...

check: build
	./bin/tgsync$(EXE) check

# Установка как сервис: systemd (Linux) или launchd (macOS), см. docs/ru/setup.md.
# На Windows: powershell -ExecutionPolicy Bypass -File scripts/install.ps1
install:
	./scripts/install.sh

uninstall:
	./scripts/uninstall.sh

ifeq ($(UNAME),Darwin)
status:
	launchctl print gui/$$(id -u)/dev.tgsync

logs:
	tail -f "$$HOME/Library/Logs/tgsync.log"
else
status:
	systemctl --user status tgsync --no-pager

logs:
	journalctl --user -u tgsync -f
endif

BINARY  := bin/trade-tracker
CMD     := ./cmd/trade-tracker
RELEASE := bin/trade-tracker-linux

TUI_BINARY := bin/trade-tracker-tui
TUI_CMD    := ./cmd/trade-tracker-tui

REMOTE_DIR   := ~/trade-tracker
COMPOSE_FILE := ~/docker-compose.yml

-include .env

HOST ?= apollo

.PHONY: all fmt vet lint test build build-server build-tui release-server deploy clean proto db-reset

all: build

proto:
	buf generate

fmt:
	gofmt -l -w .

vet: fmt
	go vet ./...

lint: vet
	golangci-lint run ./...

test: lint
	go test ./...

build: test
	go build -o $(BINARY) $(CMD)
	go build -o $(TUI_BINARY) $(TUI_CMD)

build-server: test
	go build -o $(BINARY) $(CMD)

build-tui: test
	go build -o $(TUI_BINARY) $(TUI_CMD)

release-server:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(RELEASE) $(CMD)

deploy: release-server
	rsync -az Dockerfile $(RELEASE) $(HOST):$(REMOTE_DIR)/
	ssh $(HOST) "docker build -t trade-tracker $(REMOTE_DIR) && docker compose -f $(COMPOSE_FILE) up -d trade-tracker"

clean:
	rm -f $(BINARY) $(RELEASE) $(TUI_BINARY)

db-reset:
	ssh $(HOST) "docker compose -f $(COMPOSE_FILE) down trade-tracker && docker volume rm swtsn_trade-tracker-data && docker compose -f $(COMPOSE_FILE) up -d trade-tracker"

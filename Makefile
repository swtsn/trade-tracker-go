BINARY  := bin/trade-tracker
CMD     := ./cmd/trade-tracker
RELEASE := bin/trade-tracker-linux

REMOTE_DIR   := ~/trade-tracker
COMPOSE_FILE := ~/docker-compose.yml

-include .env

.PHONY: all fmt vet lint test build release-server deploy clean proto

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

release-server:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(RELEASE) $(CMD)

deploy: release-server
	rsync -az Dockerfile $(RELEASE) $(HOST):$(REMOTE_DIR)/
	ssh $(HOST) "docker build -t trade-tracker $(REMOTE_DIR) && docker compose -f $(COMPOSE_FILE) up -d trade-tracker"

clean:
	rm -f $(BINARY) $(RELEASE)

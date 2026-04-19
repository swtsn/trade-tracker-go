BINARY := bin/trade-tracker
CMD     := ./cmd/trade-tracker

.PHONY: all fmt vet lint test build clean proto

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

clean:
	rm -f $(BINARY)

BINARY    := little-tyke
BUILD_DIR := build
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -ldflags "-X github.com/Blink-Build-Studios/little-tyke/cmd/little-tyke/cmd/version.Version=$(VERSION)"

.PHONY: build test lint vet tidy run chat chat-fast clean docker-build

build:
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/little-tyke

test:
	go test ./...

lint:
	golangci-lint run ./...

vet:
	go vet ./...

tidy:
	go mod tidy

run: build
	$(BUILD_DIR)/$(BINARY) serve

chat: build
	$(BUILD_DIR)/$(BINARY) chat $(ARGS)

chat-fast: build
	$(BUILD_DIR)/$(BINARY) chat --fast

clean:
	rm -rf $(BUILD_DIR)

docker-build:
	docker build -f docker/Dockerfile -t little-tyke:latest .

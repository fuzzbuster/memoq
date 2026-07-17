.PHONY: all build test clean vet fmt

BINARY_NAME := memoq
BUILD_DIR := bin

all: build

build:
	go build -o $(BUILD_DIR)/$(BINARY_NAME) .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

clean:
	rm -rf $(BUILD_DIR)
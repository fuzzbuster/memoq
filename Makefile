.PHONY: all build test clean vet fmt dist

BINARY_NAME := memoq
BUILD_DIR := bin
DIST_DIR := dist
GOFLAGS ?=
LDFLAGS ?= -s -w
BUILD_FLAGS := -trimpath -ldflags "$(LDFLAGS)"

all: build

build:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 go build $(GOFLAGS) $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) .

dist:
	mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) $(BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY_NAME)_linux_amd64 .
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build $(GOFLAGS) $(BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY_NAME)_linux_arm64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build $(GOFLAGS) $(BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY_NAME)_darwin_amd64 .
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build $(GOFLAGS) $(BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY_NAME)_darwin_arm64 .
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build $(GOFLAGS) $(BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY_NAME)_windows_amd64.exe .
	CGO_ENABLED=0 GOOS=windows GOARCH=arm64 go build $(GOFLAGS) $(BUILD_FLAGS) -o $(DIST_DIR)/$(BINARY_NAME)_windows_arm64.exe .

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

clean:
	rm -rf $(BUILD_DIR) $(DIST_DIR)

all: install

LD_FLAGS = -w -s -X github.com/cosmos/cosmos-sdk/types.DBBackend=pebbledb
BUILD_FLAGS := -ldflags '$(LD_FLAGS)'
BUILD_TAGS := -tags pebbledb

build:
	@echo "Building cosmos-pruner"
	@go build $(BUILD_TAGS) -mod readonly $(BUILD_FLAGS) -o build/cosmos-pruner main.go

install:
	@echo "Installing cosmos-pruner"
	@go install $(BUILD_TAGS) -mod readonly $(BUILD_FLAGS) ./...

test:
	@go test $(BUILD_TAGS) ./...

clean:
ifeq ($(OS),Windows_NT)
	@if exist build rmdir /s /q build
else
	rm -rf build
endif

.PHONY: all test install clean build

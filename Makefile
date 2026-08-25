PLUGIN_NAME := cliproxyapi-cursor-provider
OUT_DIR := build/plugins/linux/amd64
VERSION ?= dev

.PHONY: build test clean

build:
	mkdir -p $(OUT_DIR)
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -buildmode=c-shared -trimpath -ldflags="-s -w -X main.pluginVersion=$(VERSION)" -o $(OUT_DIR)/$(PLUGIN_NAME).so ./cmd/$(PLUGIN_NAME)

test:
	go test ./...

clean:
	rm -rf build dist

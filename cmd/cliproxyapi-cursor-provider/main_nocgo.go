//go:build !cgo

package main

import (
	"encoding/json"
	"fmt"

	"github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/internal/provider"
)

var pluginService = provider.New()

func main() {}

func callHost(method string, _ any) (json.RawMessage, error) {
	return nil, fmt.Errorf("host callback %s is unavailable without cgo", method)
}

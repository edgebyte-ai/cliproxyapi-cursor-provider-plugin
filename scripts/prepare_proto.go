//go:build prepareproto

package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Cursor's extracted descriptor contains top-level message names such as
// Parent_Approved that collide with protoc-gen-go's oneof wrapper names.
// Renaming descriptor type symbols does not change protobuf field numbers or
// wire bytes. This deterministic preprocessing keeps Go code generation stable.
func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: prepare_proto <input.proto> <output.proto>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	text := string(raw)
	pattern := regexp.MustCompile(`(?m)^message\s+([A-Za-z][A-Za-z0-9]*_[A-Za-z0-9_]+)\s*\{`)
	matches := pattern.FindAllStringSubmatch(text, -1)
	names := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		name := match[1]
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	for _, name := range names {
		word := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
		text = word.ReplaceAllString(text, "Wire_"+name)
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if err := os.WriteFile(os.Args[2], []byte(text), 0o644); err != nil {
		panic(err)
	}
}

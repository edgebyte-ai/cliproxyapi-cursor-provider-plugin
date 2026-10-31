package provider

import (
	"testing"

	"github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/internal/pb"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestUsageFromTurnEndedDecodesCurrentCursorWire(t *testing.T) {
	// Cursor Agent 2026.10.16: optional int64 input, output, cache read,
	// cache write, and reasoning counts; field 6 is the turn-end timestamp.
	raw := []byte{0x08, 0xe8, 0x07, 0x10, 0x28, 0x18, 0xbc, 0x05, 0x20, 0x64, 0x28, 0x0c, 0x30, 0x7b}
	raw = protowire.AppendTag(raw, 7, protowire.BytesType)
	raw = protowire.AppendString(raw, "future metadata")
	var update pb.TurnEndedUpdate
	if err := proto.Unmarshal(raw, &update); err != nil {
		t.Fatal(err)
	}
	got, err := usageFromTurnEnded(&update)
	want := tokenUsage{InputTokens: 1000, OutputTokens: 40, CacheReadTokens: 700, CacheWriteTokens: 100, ReasoningTokens: 12, Reported: true}
	if err != nil || got != want {
		t.Fatalf("usage = %+v, err = %v; want %+v", got, err, want)
	}
}

func TestUsageFromTurnEndedDistinguishesMissingAndExplicitZero(t *testing.T) {
	for _, update := range []*pb.TurnEndedUpdate{nil, {}} {
		got, err := usageFromTurnEnded(update)
		if err != nil || got != (tokenUsage{}) {
			t.Fatalf("missing usage = %+v, err = %v", got, err)
		}
	}
	for _, tc := range []struct {
		name string
		raw  []byte
		want tokenUsage
	}{
		{name: "explicit zero", raw: []byte{0x10, 0x00}, want: tokenUsage{Reported: true}},
		{name: "timestamp only", raw: []byte{0x30, 0x7b}},
		{name: "last scalar wins", raw: []byte{0x10, 0x07, 0x10, 0x09}, want: tokenUsage{OutputTokens: 9, Reported: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var update pb.TurnEndedUpdate
			if err := proto.Unmarshal(tc.raw, &update); err != nil {
				t.Fatal(err)
			}
			got, err := usageFromTurnEnded(&update)
			if err != nil || got != tc.want {
				t.Fatalf("usage = %+v, err = %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestUsageFromTurnEndedRejectsMalformedCounts(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{name: "truncated tag", raw: []byte{0x80}},
		{name: "invalid field number", raw: []byte{0x00}},
		{name: "truncated count", raw: []byte{0x08, 0x80}},
		{name: "wrong wire type", raw: []byte{0x0a, 0x00}},
		{name: "negative count", raw: protowire.AppendVarint([]byte{0x08}, ^uint64(0))},
		{name: "truncated future field", raw: []byte{0x3a, 0x02, 0x01}},
		{name: "no partial usage on error", raw: []byte{0x10, 0x05, 0x08, 0x80}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var update pb.TurnEndedUpdate
			update.ProtoReflect().SetUnknown(tc.raw)
			if got, err := usageFromTurnEnded(&update); err == nil || got != (tokenUsage{}) {
				t.Fatalf("usage = %+v, err = %v; want zero usage and error", got, err)
			}
		})
	}
}

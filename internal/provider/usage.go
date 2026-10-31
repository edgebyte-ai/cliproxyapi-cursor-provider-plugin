package provider

import (
	"fmt"
	"math"

	"github.com/edgebyte-ai/cliproxyapi-cursor-provider-plugin/internal/pb"
	"google.golang.org/protobuf/encoding/protowire"
)

type tokenUsage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	ReasoningTokens  int64
	Reported         bool
}

func (u *tokenUsage) observe(event TurnEvent) {
	if event.Usage.Reported {
		*u = event.Usage
	} else if !u.Reported {
		u.OutputTokens += int64(event.Tokens)
	}
}

func usageBlock(usage tokenUsage) map[string]any {
	usage.OutputTokens = max(usage.OutputTokens, 0)
	return map[string]any{
		"prompt_tokens": usage.InputTokens, "completion_tokens": usage.OutputTokens,
		"total_tokens":              usage.InputTokens + usage.OutputTokens,
		"prompt_tokens_details":     map[string]any{"cached_tokens": usage.CacheReadTokens, "cache_write_tokens": usage.CacheWriteTokens},
		"completion_tokens_details": map[string]any{"reasoning_tokens": usage.ReasoningTokens},
	}
}

func responseUsageBlock(usage tokenUsage) map[string]any {
	usage.OutputTokens = max(usage.OutputTokens, 0)
	return map[string]any{
		"input_tokens": usage.InputTokens, "output_tokens": usage.OutputTokens,
		"total_tokens":          usage.InputTokens + usage.OutputTokens,
		"input_tokens_details":  map[string]any{"cached_tokens": usage.CacheReadTokens, "cache_write_tokens": usage.CacheWriteTokens},
		"output_tokens_details": map[string]any{"reasoning_tokens": usage.ReasoningTokens},
	}
}

func claudeUsageBlock(usage tokenUsage) map[string]any {
	return map[string]any{
		"input_tokens":            max(usage.InputTokens-usage.CacheReadTokens-usage.CacheWriteTokens, 0),
		"output_tokens":           max(usage.OutputTokens, 0),
		"cache_read_input_tokens": usage.CacheReadTokens, "cache_creation_input_tokens": usage.CacheWriteTokens,
		"output_tokens_details": map[string]any{"thinking_tokens": usage.ReasoningTokens},
	}
}

func usageFromTurnEnded(update *pb.TurnEndedUpdate) (tokenUsage, error) {
	var usage tokenUsage
	if update == nil {
		return usage, nil
	}
	// Cursor added these optional int64 fields after the bundled wire descriptor.
	raw := update.ProtoReflect().GetUnknown()
	for len(raw) > 0 {
		number, wireType, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return tokenUsage{}, fmt.Errorf("decode Cursor token usage: %w", protowire.ParseError(n))
		}
		raw = raw[n:]
		var target *int64
		switch number {
		case 1:
			// Input includes both cache reads and cache writes.
			target = &usage.InputTokens
		case 2:
			target = &usage.OutputTokens
		case 3:
			target = &usage.CacheReadTokens
		case 4:
			target = &usage.CacheWriteTokens
		case 5:
			target = &usage.ReasoningTokens
		}
		if target != nil {
			if wireType != protowire.VarintType {
				return tokenUsage{}, fmt.Errorf("decode Cursor token usage: field %d has wire type %d", number, wireType)
			}
			value, n := protowire.ConsumeVarint(raw)
			if n < 0 {
				return tokenUsage{}, fmt.Errorf("decode Cursor token usage: %w", protowire.ParseError(n))
			}
			if value > math.MaxInt64 {
				return tokenUsage{}, fmt.Errorf("decode Cursor token usage: field %d is negative", number)
			}
			*target = int64(value)
			usage.Reported = true
			raw = raw[n:]
			continue
		}
		n = protowire.ConsumeFieldValue(number, wireType, raw)
		if n < 0 {
			return tokenUsage{}, fmt.Errorf("decode Cursor token usage: %w", protowire.ParseError(n))
		}
		raw = raw[n:]
	}
	return usage, nil
}

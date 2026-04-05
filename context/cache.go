package context

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

// PromptCacheFingerprint captures the parts of a request that should stay
// stable for prefix-cache reuse.
type PromptCacheFingerprint struct {
	PrefixHash     string `json:"prefix_hash,omitzero"`
	ToolSchemaHash string `json:"tool_schema_hash,omitzero"`
}

// FingerprintPromptCache hashes the static request prefix and current tool
// schema list.
//
// The prefix hash intentionally excludes the session history suffix so the
// result stays stable across ordinary turn-to-turn conversation growth. The
// tool hash is taken from the built request so lazy tool unlocking is visible.
func FingerprintPromptCache(
	sess *session.Session,
	req *llm.Request,
) (PromptCacheFingerprint, error) {
	if req == nil {
		return PromptCacheFingerprint{}, nil
	}

	prefix := req.Messages
	if sess != nil {
		history, err := sess.EffectiveMessages()
		if err != nil {
			return PromptCacheFingerprint{}, err
		}
		if n := len(req.Messages) - len(history); n >= 0 && n <= len(req.Messages) {
			prefix = req.Messages[:n]
		}
	}

	return PromptCacheFingerprint{
		PrefixHash:     hashValue(prefix),
		ToolSchemaHash: hashValue(req.Tools),
	}, nil
}

func hashValue(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}

	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (f PromptCacheFingerprint) String() string {
	switch {
	case f.PrefixHash == "" && f.ToolSchemaHash == "":
		return ""
	case f.ToolSchemaHash == "":
		return f.PrefixHash
	default:
		return fmt.Sprintf("%s/%s", f.PrefixHash, f.ToolSchemaHash)
	}
}

// CacheAligner returns a RequestProcessor that adds provider-agnostic
// cache-control markers to the request.
//
// For Anthropic, it marks the first n messages and the tool list with
// "ephemeral" cache-control. Typically, n should be small (e.g. 1-3) to
// capture the system prompt and initial task context without hitting the
// 4-breakpoint limit.
func CacheAligner(messageLimit int) RequestProcessor {
	return RequestProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.Request) error {
			if req == nil {
				return nil
			}

			// Anthropic caching: mark first n messages.
			for i := 0; i < len(req.Messages) && i < messageLimit; i++ {
				req.Messages[i].CacheControl = &llm.CacheControl{Type: "ephemeral"}
			}

			// Mark tool list if present.
			if len(req.Tools) > 0 {
				// Most providers cache the whole list as one block if any tool is marked,
				// or have a specific top-level toggle. Canto marks all as a hint.
				for i := range req.Tools {
					req.Tools[i].CacheControl = &llm.CacheControl{Type: "ephemeral"}
				}
			}

			return nil
		},
	)
}

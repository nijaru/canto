package context

import (
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

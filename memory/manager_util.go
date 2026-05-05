package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

func filterRoles(roles []Role, keep func(Role) bool) []Role {
	if len(roles) == 0 {
		return nil
	}
	var out []Role
	for _, role := range roles {
		if keep(role) {
			out = append(out, role)
		}
	}
	return out
}

func memoryID(candidate Candidate) string {
	key := candidate.Key
	if key == "" {
		key = candidate.Content
	}
	raw := strings.Join([]string{
		string(candidate.Namespace.Scope),
		candidate.Namespace.ID,
		string(candidate.Role),
		key,
	}, ":")
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func coreBlockMemoryID(namespace Namespace, name string) string {
	if name == "" {
		name = "default"
	}
	return strings.Join([]string{
		string(namespace.Scope),
		namespace.ID,
		string(RoleCore),
		name,
	}, ":")
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func mergeMaps(base, extra map[string]any) map[string]any {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := cloneMap(base)
	if out == nil {
		out = map[string]any{}
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func cloneTime(src *time.Time) *time.Time {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}

func inheritMemoryLifecycle(dst *Memory, existing *Memory) {
	if dst.ObservedAt == nil {
		dst.ObservedAt = cloneTime(existing.ObservedAt)
	}
	if dst.ValidFrom == nil {
		dst.ValidFrom = cloneTime(existing.ValidFrom)
	}
	if dst.ValidTo == nil {
		dst.ValidTo = cloneTime(existing.ValidTo)
	}
	if dst.Supersedes == "" {
		dst.Supersedes = existing.Supersedes
	}
	if dst.SupersededBy == "" {
		dst.SupersededBy = existing.SupersededBy
	}
	if dst.ForgottenAt == nil {
		dst.ForgottenAt = cloneTime(existing.ForgottenAt)
	}
}

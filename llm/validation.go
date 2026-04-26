package llm

import "fmt"

// ValidateRequest checks provider-facing invariants for unified LLM requests.
func ValidateRequest(req *Request) error {
	if req == nil {
		return nil
	}

	seenTranscript := false
	for i, msg := range req.Messages {
		if isPrivilegedRole(msg.Role) {
			if seenTranscript {
				return fmt.Errorf(
					"llm request: privileged %q message at index %d after transcript messages",
					msg.Role,
					i,
				)
			}
			continue
		}
		seenTranscript = true
	}
	return nil
}

func isPrivilegedRole(role Role) bool {
	return role == RoleSystem || role == RoleDeveloper
}

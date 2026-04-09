package safety

import "strings"

// EnvSanitizer scrubs sensitive environment variables before subprocess
// execution while preserving a narrow allowlist of operational defaults.
type EnvSanitizer struct {
	Allow []string
	Deny  []string
}

// NewEnvSanitizer returns a sanitizer with conservative defaults.
func NewEnvSanitizer() *EnvSanitizer {
	return &EnvSanitizer{
		Allow: []string{
			"HOME",
			"LANG",
			"LC_ALL",
			"LC_CTYPE",
			"LOGNAME",
			"PATH",
			"PWD",
			"SHELL",
			"TERM",
			"TMPDIR",
			"USER",
		},
		Deny: []string{
			"API_KEY",
			"AUTH",
			"COOKIE",
			"CREDENTIAL",
			"PASSWORD",
			"SECRET",
			"SESSION",
			"TOKEN",
		},
	}
}

// Sanitize returns a filtered copy of env.
func (s *EnvSanitizer) Sanitize(env []string) []string {
	if s == nil {
		return append([]string(nil), env...)
	}
	allow := make(map[string]struct{}, len(s.Allow))
	for _, name := range s.Allow {
		allow[strings.ToUpper(name)] = struct{}{}
	}

	out := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			continue
		}
		upper := strings.ToUpper(name)
		if _, ok := allow[upper]; ok || !s.denied(upper) {
			out = append(out, entry)
		}
	}
	return out
}

func (s *EnvSanitizer) denied(name string) bool {
	for _, deny := range s.Deny {
		if strings.Contains(name, strings.ToUpper(deny)) {
			return true
		}
	}
	return false
}

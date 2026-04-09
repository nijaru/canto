package safety_test

import (
	"slices"
	"testing"

	"github.com/nijaru/canto/safety"
)

func TestEnvSanitizerSanitizeScrubsSecretsByDefault(t *testing.T) {
	sanitizer := safety.NewEnvSanitizer()

	got := sanitizer.Sanitize([]string{
		"PATH=/usr/bin",
		"HOME=/tmp/home",
		"OPENAI_API_KEY=secret",
		"GITHUB_TOKEN=secret",
		"SAFE_VAR=ok",
	})

	if !slices.Contains(got, "PATH=/usr/bin") || !slices.Contains(got, "HOME=/tmp/home") {
		t.Fatalf("expected safe defaults to survive, got %#v", got)
	}
	if !slices.Contains(got, "SAFE_VAR=ok") {
		t.Fatalf("expected non-secret var to survive, got %#v", got)
	}
	if slices.Contains(got, "OPENAI_API_KEY=secret") ||
		slices.Contains(got, "GITHUB_TOKEN=secret") {
		t.Fatalf("expected secret vars to be scrubbed, got %#v", got)
	}
}

func TestEnvSanitizerSanitizeRespectsAllowlist(t *testing.T) {
	sanitizer := &safety.EnvSanitizer{
		Allow: []string{"PATH", "SSH_AUTH_SOCK"},
		Deny:  []string{"AUTH", "TOKEN"},
	}

	got := sanitizer.Sanitize([]string{
		"PATH=/usr/bin",
		"SSH_AUTH_SOCK=/tmp/socket",
		"SESSION_TOKEN=secret",
	})

	if !slices.Contains(got, "SSH_AUTH_SOCK=/tmp/socket") {
		t.Fatalf("expected allowlisted auth sock to survive, got %#v", got)
	}
	if slices.Contains(got, "SESSION_TOKEN=secret") {
		t.Fatalf("expected denied token to be scrubbed, got %#v", got)
	}
}

// Package skill integrates the standalone github.com/nijaru/agentskills module
// with Canto's tool and prompt APIs.
//
// The reusable SKILL.md loader, registry, validation, and model types live in
// the external agentskills module. This package owns the Canto-specific
// runtime tools and request-time prompt helpers that operate on that shared core.
package skill

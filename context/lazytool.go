package context

import (
	"context"
	"strings"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
	"github.com/nijaru/canto/tool"
)

const (
	// DefaultLazyThreshold is the tool count above which lazy loading activates.
	DefaultLazyThreshold = 20
	searchToolName       = "search_tools"
)

// LazyToolProcessor conditionally loads tool specs.
//
// If the registry has <= Threshold tools, all specs are included (same as
// ToolProcessor). Above the threshold, only the search_tools meta-tool is
// exposed, along with a system hint listing all available tool names.
// Previously searched tools are re-included by scanning session history for
// search_tools results — ensuring the agent can call tools it has discovered.
//
// Wire a SearchTool (from x/tools) into the registry before using this.
type LazyToolProcessor struct {
	Registry  *tool.Registry
	Threshold int // default: DefaultLazyThreshold
}

// NewLazyToolProcessor creates a LazyToolProcessor with the given registry.
func NewLazyToolProcessor(reg *tool.Registry) *LazyToolProcessor {
	return &LazyToolProcessor{Registry: reg, Threshold: DefaultLazyThreshold}
}

func (p *LazyToolProcessor) Process(
	ctx context.Context,
	pr llm.Provider,
	model string,
	sess *session.Session,
	req *llm.LLMRequest,
) error {
	if p.Registry == nil {
		return nil
	}

	threshold := p.Threshold
	if threshold <= 0 {
		threshold = DefaultLazyThreshold
	}

	specs := p.Registry.Specs()
	if len(specs) <= threshold {
		// Below threshold: include everything.
		req.Tools = append(req.Tools, specs...)
		return nil
	}

	// Above threshold: include only the search_tools meta-tool.
	if st, ok := p.Registry.Get(searchToolName); ok {
		spec := st.Spec()
		req.Tools = append(req.Tools, &spec)
	}

	// Scan session history for search_tools results and unlock those tools.
	unlocked := p.unlockedFromHistory(sess)
	for name := range unlocked {
		if t, ok := p.Registry.Get(name); ok {
			spec := t.Spec()
			req.Tools = append(req.Tools, &spec)
		}
	}

	// Inject a system hint listing all available tool names.
	names := p.Registry.Names()
	hint := "Available tools (use search_tools to get specs): " + strings.Join(names, ", ")
	injectSystemHint(req, hint)

	return nil
}

// unlockedFromHistory scans the session for search_tools results and
// extracts the tool names that were returned. Those tools are "unlocked"
// and should be included in the next request.
func (p *LazyToolProcessor) unlockedFromHistory(sess *session.Session) map[string]struct{} {
	unlocked := make(map[string]struct{})
	for _, m := range sess.Messages() {
		if m.Role != llm.RoleTool || m.Name != searchToolName {
			continue
		}
		// Try to parse as a JSON array of ToolSpec.
		var specs []llm.ToolSpec
		if err := json.Unmarshal([]byte(m.Content), &specs); err != nil {
			continue
		}
		for _, spec := range specs {
			if spec.Name != "" && spec.Name != searchToolName {
				unlocked[spec.Name] = struct{}{}
			}
		}
	}
	return unlocked
}

// injectSystemHint prepends a system message with the hint text.
func injectSystemHint(req *llm.LLMRequest, hint string) {
	for i, m := range req.Messages {
		if m.Role == llm.RoleSystem {
			req.Messages[i].Content += "\n\n" + hint
			return
		}
	}
	// No system message yet — prepend one.
	req.Messages = append([]llm.Message{{Role: llm.RoleSystem, Content: hint}}, req.Messages...)
}

package prompt

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/memory"
	"github.com/nijaru/canto/session"
)

type MemoryPromptOptions struct {
	Namespaces        []memory.Namespace
	Roles             []memory.Role
	Limit             int
	Query             string
	UseSemantic       bool
	IncludeRecent     bool
	ValidAt           *time.Time
	ObservedAfter     *time.Time
	ObservedBefore    *time.Time
	IncludeForgotten  bool
	IncludeSuperseded bool
}

func MemoryPrompt(retriever memory.Retriever, opts MemoryPromptOptions) RequestProcessor {
	return RequestProcessorFunc(func(
		ctx context.Context,
		p llm.Provider,
		model string,
		sess *session.Session,
		req *llm.Request,
	) error {
		if retriever == nil {
			return nil
		}
		query := opts.Query
		if query == "" {
			if sess == nil {
				return nil
			}
			messages, err := sess.EffectiveMessages()
			if err != nil {
				return err
			}
			for i := len(messages) - 1; i >= 0; i-- {
				if messages[i].Content != "" {
					query = messages[i].Content
					break
				}
			}
		}
		results, err := retriever.Retrieve(ctx, memory.Query{
			Namespaces:        opts.Namespaces,
			Roles:             opts.Roles,
			Text:              query,
			Limit:             opts.Limit,
			UseSemantic:       opts.UseSemantic,
			IncludeRecent:     opts.IncludeRecent,
			ValidAt:           opts.ValidAt,
			ObservedAfter:     opts.ObservedAfter,
			ObservedBefore:    opts.ObservedBefore,
			IncludeForgotten:  opts.IncludeForgotten,
			IncludeSuperseded: opts.IncludeSuperseded,
		})
		if err != nil {
			return err
		}
		if len(results) == 0 {
			return nil
		}
		var sb strings.Builder
		sb.WriteString("<memory_context>\n")
		for _, item := range results {
			fmt.Fprintf(
				&sb,
				"[%s/%s/%s] %s\n",
				item.Namespace.Scope,
				item.Namespace.ID,
				item.Role,
				item.Content,
			)
		}
		sb.WriteString("</memory_context>")
		injectSystemBlock(req, memoryPromptRegex, sb.String())
		return nil
	})
}

var memoryPromptRegex = regexp.MustCompile(`(?s)<memory_context>.*?</memory_context>\n*`)

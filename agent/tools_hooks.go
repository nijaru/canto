package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nijaru/canto/hook"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/tool"
)

func hookContextOutput(event hook.Event, results []*hook.Result) string {
	var out strings.Builder
	for _, res := range results {
		if res == nil || res.Output == "" {
			continue
		}
		fmt.Fprintf(&out, "<hook_context name=%q>\n%s\n</hook_context>\n", event, res.Output)
	}
	return out.String()
}

func toolHookData(
	call llm.Call,
	metadata tool.Metadata,
	extra map[string]any,
) map[string]any {
	data := map[string]any{
		"tool": call.Function.Name,
		"args": call.Function.Arguments,
	}
	if metadataPresent(metadata) {
		data["metadata"] = metadata
	}
	for key, value := range extra {
		data[key] = value
	}
	return data
}

func metadataPresent(metadata tool.Metadata) bool {
	return metadata.Category != "" ||
		metadata.ReadOnly ||
		metadata.Concurrency != tool.Unknown ||
		metadata.Deferred ||
		len(metadata.Examples) > 0
}

func applyPreToolHookData(call *llm.Call, results []*hook.Result) {
	for _, res := range results {
		if res == nil {
			continue
		}
		name, ok := stringHookData(res.Data, "tool")
		if ok {
			call.Function.Name = name
		}
		args, ok := stringHookData(res.Data, "args")
		if ok {
			call.Function.Arguments = args
		}
	}
}

func applyPostToolHookData(output *string, execErr *error, results []*hook.Result) {
	for _, res := range results {
		if res == nil {
			continue
		}
		if nextOutput, ok := stringHookData(res.Data, "output"); ok {
			*output = nextOutput
		}
		if !hookDataPresent(res.Data, "error") {
			continue
		}
		nextErr, ok := stringHookData(res.Data, "error")
		if !ok || nextErr == "" {
			*execErr = nil
			continue
		}
		*execErr = errors.New(nextErr)
	}
}

func stringHookData(data map[string]any, key string) (string, bool) {
	if !hookDataPresent(data, key) {
		return "", false
	}
	value, ok := data[key].(string)
	return value, ok
}

func hookDataPresent(data map[string]any, key string) bool {
	if data == nil {
		return false
	}
	_, ok := data[key]
	return ok
}

func postHookBlockOutput(err error, currentOutput string) string {
	message := fmt.Sprintf("Error: %v", err)
	if strings.TrimSpace(currentOutput) == "" {
		return message
	}
	return strings.TrimSpace(currentOutput) + "\n" + message
}

func getHandoffTargets(r *tool.Registry) []string {
	if r == nil {
		return nil
	}
	var targets []string
	for _, spec := range r.Specs() {
		if after, ok := strings.CutPrefix(spec.Name, "transfer_to_"); ok {
			targets = append(targets, after)
		}
	}
	return targets
}

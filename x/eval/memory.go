package eval

import (
	"context"
	"strings"

	"github.com/nijaru/canto/memory"
)

type MemoryExpectation struct {
	Contains []string
	Excludes []string
}

type MemoryCase struct {
	Name   string
	Query  memory.Query
	Expect MemoryExpectation
}

type MemoryCaseResult struct {
	Name       string
	Passed     bool
	Missing    []string
	Unexpected []string
	Hits       []memory.Memory
}

func EvaluateMemoryCases(
	ctx context.Context,
	manager *memory.Manager,
	cases []MemoryCase,
) ([]MemoryCaseResult, error) {
	results := make([]MemoryCaseResult, 0, len(cases))
	for _, testCase := range cases {
		hits, err := manager.Retrieve(ctx, testCase.Query)
		if err != nil {
			return nil, err
		}
		result := MemoryCaseResult{
			Name: testCase.Name,
			Hits: hits,
		}
		blob := memoryBlob(hits)
		for _, needle := range testCase.Expect.Contains {
			if !strings.Contains(blob, needle) {
				result.Missing = append(result.Missing, needle)
			}
		}
		for _, needle := range testCase.Expect.Excludes {
			if strings.Contains(blob, needle) {
				result.Unexpected = append(result.Unexpected, needle)
			}
		}
		result.Passed = len(result.Missing) == 0 && len(result.Unexpected) == 0
		results = append(results, result)
	}
	return results, nil
}

func memoryBlob(hits []memory.Memory) string {
	var sb strings.Builder
	for _, hit := range hits {
		sb.WriteString(hit.Content)
		sb.WriteByte('\n')
	}
	return sb.String()
}

package eval

import (
	"context"
	"fmt"
	"strings"

	"github.com/nijaru/canto/memory"
)

type MemoryAssertion func([]memory.Memory) error

type MemoryExpectation struct {
	Contains []string
	Excludes []string
}

type MemoryCase struct {
	Name   string
	Query  memory.Query
	Expect MemoryExpectation
	Assert MemoryAssertion
}

type MemoryCaseResult struct {
	Name           string
	Passed         bool
	Missing        []string
	Unexpected     []string
	AssertionError string
	Hits           []memory.Memory
}

func EvaluateMemoryCases(
	ctx context.Context,
	retriever memory.Retriever,
	cases []MemoryCase,
) ([]MemoryCaseResult, error) {
	results := make([]MemoryCaseResult, 0, len(cases))
	for _, testCase := range cases {
		hits, err := retriever.Retrieve(ctx, testCase.Query)
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
		if testCase.Assert != nil {
			if err := testCase.Assert(hits); err != nil {
				result.AssertionError = err.Error()
			}
		}
		result.Passed = len(result.Missing) == 0 &&
			len(result.Unexpected) == 0 &&
			result.AssertionError == ""
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

func RequireRoles(roles ...memory.Role) MemoryAssertion {
	return func(hits []memory.Memory) error {
		for _, role := range roles {
			found := false
			for _, hit := range hits {
				if hit.Role == role {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("missing role %q", role)
			}
		}
		return nil
	}
}

func ExcludeNamespaces(namespaces ...memory.Namespace) MemoryAssertion {
	return func(hits []memory.Memory) error {
		for _, hit := range hits {
			for _, namespace := range namespaces {
				if hit.Namespace == namespace {
					return fmt.Errorf(
						"unexpected namespace %q/%q in results",
						namespace.Scope,
						namespace.ID,
					)
				}
			}
		}
		return nil
	}
}

func ExcludeIDs(ids ...string) MemoryAssertion {
	return func(hits []memory.Memory) error {
		for _, hit := range hits {
			for _, id := range ids {
				if hit.ID == id {
					return fmt.Errorf("unexpected memory id %q in results", id)
				}
			}
		}
		return nil
	}
}

func RequireNoForgotten() MemoryAssertion {
	return func(hits []memory.Memory) error {
		for _, hit := range hits {
			if hit.ForgottenAt != nil {
				return fmt.Errorf("unexpected forgotten memory %q in results", hit.ID)
			}
		}
		return nil
	}
}

func RequireNoSuperseded() MemoryAssertion {
	return func(hits []memory.Memory) error {
		for _, hit := range hits {
			if hit.SupersededBy != "" {
				return fmt.Errorf("unexpected superseded memory %q in results", hit.ID)
			}
		}
		return nil
	}
}

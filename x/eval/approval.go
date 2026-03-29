package eval

import (
	"context"

	"github.com/nijaru/canto/approval"
	"github.com/nijaru/canto/session"
)

type ApprovalCase struct {
	Name        string
	Run         func(context.Context, *approval.Manager, *session.Session) error
	ExpectError string
}

type ApprovalCaseResult struct {
	Name   string
	Passed bool
	Error  string
}

func EvaluateApprovalCases(
	ctx context.Context,
	manager *approval.Manager,
	sess *session.Session,
	cases []ApprovalCase,
) []ApprovalCaseResult {
	results := make([]ApprovalCaseResult, 0, len(cases))
	for _, testCase := range cases {
		err := testCase.Run(ctx, manager, sess)
		got := ""
		if err != nil {
			got = err.Error()
		}
		passed := (testCase.ExpectError == "" && got == "") || (testCase.ExpectError != "" && got == testCase.ExpectError)
		results = append(results, ApprovalCaseResult{
			Name:   testCase.Name,
			Passed: passed,
			Error:  got,
		})
	}
	return results
}

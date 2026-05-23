package tool

import (
	"context"
	"strings"
	"testing"

	"github.com/nijaru/canto/approval"
)

type typedWeatherArgs struct {
	City string `json:"city" jsonschema:"required"`
}

type typedWeatherResult struct {
	Forecast string `json:"forecast"`
}

func TestNewTypedExecutesTypedHandler(t *testing.T) {
	weather, err := NewTyped(TypedConfig[typedWeatherArgs, typedWeatherResult]{
		Name:        "weather",
		Description: "Get weather.",
		Metadata: Metadata{
			Category: "service",
			ReadOnly: true,
		},
		Execute: func(_ context.Context, args typedWeatherArgs) (typedWeatherResult, error) {
			if args.City != "Paris" {
				t.Fatalf("city = %q, want Paris", args.City)
			}
			return typedWeatherResult{Forecast: "clear"}, nil
		},
		Approval: func(args typedWeatherArgs) (approval.Requirement, bool, error) {
			return approval.Requirement{
				Category:  "service",
				Operation: "lookup",
				Resource:  args.City,
			}, true, nil
		},
	})
	if err != nil {
		t.Fatalf("NewTyped: %v", err)
	}

	if weather.Spec().Name != "weather" {
		t.Fatalf("spec name = %q, want weather", weather.Spec().Name)
	}
	if weather.Spec().Parameters == nil {
		t.Fatal("schema was not inferred")
	}
	if got := MetadataFor(weather); got.Category != "service" || !got.ReadOnly {
		t.Fatalf("metadata = %#v", got)
	}

	out, err := weather.Execute(t.Context(), `{"city":"Paris"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, `"forecast":"clear"`) {
		t.Fatalf("output = %q, want JSON forecast", out)
	}

	req, ok, err := weather.ApprovalRequirement(`{"city":"Paris"}`)
	if err != nil {
		t.Fatalf("ApprovalRequirement: %v", err)
	}
	if !ok || req.Resource != "Paris" {
		t.Fatalf("approval = %#v, %v", req, ok)
	}
}

func TestRegisterTypedRequiresRegistry(t *testing.T) {
	_, err := RegisterTyped[typedWeatherArgs, typedWeatherResult](
		nil,
		TypedConfig[typedWeatherArgs, typedWeatherResult]{
			Name:        "weather",
			Description: "Get weather.",
			Execute: func(context.Context, typedWeatherArgs) (typedWeatherResult, error) {
				return typedWeatherResult{}, nil
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "registry is required") {
		t.Fatalf("RegisterTyped error = %v", err)
	}
}

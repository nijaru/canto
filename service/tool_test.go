package service

import (
	"context"
	"errors"
	"testing"

	"github.com/go-json-experiment/json"

	"github.com/nijaru/canto/safety"
	"github.com/nijaru/canto/tool"
)

type weatherArgs struct {
	City string `json:"city" jsonschema:"city name"`
}

type weatherResult struct {
	Forecast string `json:"forecast"`
}

func TestToolExecutesTypedHandler(t *testing.T) {
	weather, err := New(Config[weatherArgs, weatherResult]{
		Name:        "get_weather",
		Description: "Fetch current weather.",
		Metadata: tool.Metadata{
			Category:    "service",
			ReadOnly:    true,
			Concurrency: tool.Parallel,
		},
		Execute: func(_ context.Context, args weatherArgs) (weatherResult, error) {
			return weatherResult{Forecast: "sunny in " + args.City}, nil
		},
		Approval: ReadOnly("weather.get", func(args weatherArgs) string {
			return args.City
		}),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	spec := weather.Spec()
	if spec.Name != "get_weather" {
		t.Fatalf("name = %q, want get_weather", spec.Name)
	}
	params, ok := spec.Parameters.(map[string]any)
	if !ok {
		t.Fatalf("parameters type = %T, want map[string]any", spec.Parameters)
	}
	properties, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties type = %T, want map[string]any", params["properties"])
	}
	if _, ok := properties["city"]; !ok {
		t.Fatal("expected inferred city property")
	}

	out, err := weather.Execute(t.Context(), `{"city":"Paris"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result weatherResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Forecast != "sunny in Paris" {
		t.Fatalf("forecast = %q, want sunny in Paris", result.Forecast)
	}

	req, ok, err := weather.ApprovalRequirement(`{"city":"Paris"}`)
	if err != nil {
		t.Fatalf("ApprovalRequirement: %v", err)
	}
	if !ok {
		t.Fatal("expected approval requirement")
	}
	if req.Category != string(safety.CategoryRead) ||
		req.Operation != "weather.get" ||
		req.Resource != "Paris" {
		t.Fatalf("approval = %#v, want read weather.get Paris", req)
	}
}

func TestToolRetriesTransientErrors(t *testing.T) {
	transient := errors.New("temporary service error")
	attempts := 0
	api, err := New(Config[weatherArgs, weatherResult]{
		Name:        "retry_weather",
		Description: "Fetch weather with retry.",
		Execute: func(_ context.Context, args weatherArgs) (weatherResult, error) {
			attempts++
			if attempts == 1 {
				return weatherResult{}, transient
			}
			return weatherResult{Forecast: args.City}, nil
		},
		Retry: RetryPolicy{
			MaxAttempts: 2,
			Retryable: func(err error) bool {
				return errors.Is(err, transient)
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	out, err := api.Execute(t.Context(), `{"city":"Berlin"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	var result weatherResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Forecast != "Berlin" {
		t.Fatalf("forecast = %q, want Berlin", result.Forecast)
	}
}

func TestRegisterAddsTool(t *testing.T) {
	registry := tool.NewRegistry()
	_, err := Register(registry, Config[weatherArgs, weatherResult]{
		Name:        "registered_weather",
		Description: "Fetch weather.",
		Execute: func(_ context.Context, args weatherArgs) (weatherResult, error) {
			return weatherResult{Forecast: args.City}, nil
		},
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, ok := registry.Get("registered_weather"); !ok {
		t.Fatal("expected registered_weather tool")
	}
}

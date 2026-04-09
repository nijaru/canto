package llm

import (
	"context"
	"slices"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func resetUsageMetricsForTest() {
	usageMetricsOnce = sync.Once{}
	usageMetricsOK = false

	inputTokensCounter = nil
	outputTokensCounter = nil
	totalTokensCounter = nil
	costCounter = nil

	inputTokensHistogram = nil
	outputTokensHistogram = nil
	totalTokensHistogram = nil
	costHistogram = nil
}

func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	return rm
}

func TestRecordUsage_EmitsCountersAndHistograms(t *testing.T) {
	resetUsageMetricsForTest()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)
	t.Cleanup(func() {
		_ = mp.Shutdown(context.Background())
		resetUsageMetricsForTest()
	})

	RecordUsage(t.Context(), "anthropic", "claude-test", Usage{
		InputTokens:  10,
		OutputTokens: 5,
		TotalTokens:  15,
		Cost:         0.125,
	})

	rm := collectMetrics(t, reader)
	names := make([]string, 0, 8)
	for _, scope := range rm.ScopeMetrics {
		for _, metric := range scope.Metrics {
			names = append(names, metric.Name)
		}
	}

	want := []string{
		"gen_ai.usage.cost",
		"gen_ai.usage.cost.per_call",
		"gen_ai.usage.input_tokens",
		"gen_ai.usage.input_tokens.per_call",
		"gen_ai.usage.output_tokens",
		"gen_ai.usage.output_tokens.per_call",
		"gen_ai.usage.total_tokens",
		"gen_ai.usage.total_tokens.per_call",
	}
	for _, name := range want {
		if !slices.Contains(names, name) {
			t.Fatalf("metric %q not found in %#v", name, names)
		}
	}
}

func TestRecordUsage_DerivesTotalTokensWhenUnset(t *testing.T) {
	resetUsageMetricsForTest()

	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(mp)
	t.Cleanup(func() {
		_ = mp.Shutdown(context.Background())
		resetUsageMetricsForTest()
	})

	RecordUsage(t.Context(), "openai", "gpt-test", Usage{
		InputTokens:  12,
		OutputTokens: 8,
		Cost:         0.5,
	})

	rm := collectMetrics(t, reader)
	for _, scope := range rm.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != "gen_ai.usage.total_tokens" {
				continue
			}
			sum, ok := metric.Data.(metricdata.Sum[int64])
			if !ok || len(sum.DataPoints) != 1 {
				t.Fatalf("unexpected total token metric data: %#v", metric.Data)
			}
			if got := sum.DataPoints[0].Value; got != 20 {
				t.Fatalf("total token counter = %d, want 20", got)
			}
			return
		}
	}
	t.Fatal("gen_ai.usage.total_tokens metric not found")
}

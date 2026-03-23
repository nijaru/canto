#!/usr/bin/env bash
set -e

# Run the tests to ensure correctness first.
# If tests fail, the benchmark script exits early with a non-zero status.
go test -v ./target_test.go ./target.go >/dev/null 2>&1 || {
	echo "ERROR: Tests failed or code did not compile."
	exit 1
}

# Run the benchmark.
# Capture the ns/op (nanoseconds per operation) metric.
# Format: BenchmarkContainsAny-12    1000000    1234 ns/op
OUTPUT=$(go test -bench=. -benchmem ./target_test.go ./target.go)

# Extract the ns/op value using awk. Lower is better.
SCORE=$(echo "$OUTPUT" | awk '/BenchmarkContainsAny/ {print $3}')

if [ -z "$SCORE" ]; then
	echo "ERROR: Failed to extract benchmark score."
	exit 1
fi

# Print exactly this format so the harness can parse it reliably.
echo "SCORE: $SCORE"

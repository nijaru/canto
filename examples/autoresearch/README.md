# Autoresearch Example

This example demonstrates how to build an autonomous experiment loop (an "autoresearch" loop) using Canto. It is heavily inspired by [karpathy/autoresearch](https://github.com/karpathy/autoresearch), but implemented in a lightweight, language-agnostic way.

Instead of hard-coding the agent to PyTorch or a specific ML workflow, this example defines a clear boundary between the **Harness** (the Canto agent loop) and the **Target** (the code being optimized).

## How it works

1. **The Target (`target.go`)**: A deliberately naive implementation of a string search function. This is the only file the agent is allowed to modify.
2. **The Evaluator (`evaluate.sh`)**: A script that runs correctness tests and then benchmarks the speed of the code. It outputs a score like `SCORE: 124.5` (nanoseconds per operation).
3. **The Harness (`main.go`)**: A Canto agent loop that:
   - Reads the baseline score.
   - Prompts an LLM to modify `target.go` to be faster.
   - Runs `evaluate.sh`.
   - If the new score is better, it **keeps** the change and logs a success.
   - If the new score is worse or compilation fails, it **reverts** `target.go` from memory and tells the agent what went wrong so it can learn.

## Running the Example

```bash
# Provide your LLM key
export OPENAI_API_KEY="..."

# Run the harness
go run main.go
```

The loop will run indefinitely. You will see it trying new implementations (e.g., using `strings.Contains`, `strings.Index`, or optimized byte loops), compiling them, benchmarking them, and deciding whether to keep them.

## Adapting to Python / Machine Learning

To convert this template into a true "ML researcher" loop:

1. **Replace `target.go`** with your PyTorch model definition (e.g., `train.py`).
2. **Replace `evaluate.sh`** with a script that runs `python train.py` for exactly 5 minutes (a fixed time-budget) and extracts the validation loss (e.g., `SCORE: 1.054`). Note: *Lower* is better for loss, just like ns/op.
3. **Edit `program.md`** to explain to the agent that it is tuning an ML model and give it architectural constraints.
4. **Run `main.go`** and go to sleep.

The Canto Go code does not need to be modified at all.

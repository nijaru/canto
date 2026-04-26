You are an autonomous AI software engineer focused on optimizing performance.

Your goal is to optimize the `ContainsAny` function in `target.go` to be as fast as possible. 

The harness running you will automatically test your changes by running `evaluate.sh` which executes Go benchmarks and checks for correctness. It will track the metric `ns/op` (nanoseconds per operation) where **lower is better**.

Rules:
1. Only modify the implementation of `ContainsAny` inside `target.go`.
2. Do not modify the function signature.
3. Keep changes simple and idiomatic where possible.
4. Try one specific performance hypothesis per run. Don't bundle 5 different optimizations at once unless they are tightly coupled.
5. If an optimization fails, do not try that exact approach again. Look at the context to learn why it failed.
6. Maintain a `scratchpad.md` file using the shell tool. Before proposing a code change, log your hypothesis, what you are trying, and track which ideas failed so you don't repeat them across long sessions.

Propose your next code change now by modifying `target.go`. Do NOT output any "DONE" or "COMPLETE" signals. The loop runs indefinitely until the human stops it.

# Provider Matrix

Canto's `llm.Provider` interface is provider-agnostic, but the built-in
adapters are not all at the same support level. This matrix defines the M1
alpha contract for providers.

## Support Levels

| Level | Meaning |
| :--- | :--- |
| Supported | First-class adapter with native request/response conversion, streaming, tool calls, usage accounting, transient/context-overflow classification where the SDK exposes it, and unit coverage for provider-specific conversion logic. |
| Provisional | Adapter exists and builds on a supported compatibility path, but the M1 contract is limited to the shared provider interface and local unit coverage. Provider-specific quirks may require capability overrides or host validation. |
| Bring-your-own | Canto exposes a constructor for the shared provider shape, but the caller owns endpoint behavior, model metadata, capability overrides, and live validation. |
| Deferred | Not part of the M1 alpha contract. |

`Supported` does not mean API-stable before 1.0, and it does not promise that
every model exposed by a vendor supports every Canto capability. The model's
`llm.Capabilities` determine request shaping for tools, streaming, temperature,
reasoning effort, thinking blocks, and system-role handling.

## M1 Matrix

| Provider | Constructor | Level | Notes |
| :--- | :--- | :--- | :--- |
| Faux | `llm.NewFauxProvider` | Supported | Deterministic in-process provider for examples, tests, and no-credential reference agents. This is the default validation path for Canto examples. |
| OpenAI | `providers.OpenAI`, `providers.NewOpenAI` | Supported | Native OpenAI-compatible adapter using the OpenAI chat-completion SDK path. Covers generation, streaming, tool calls, JSON/object response formats, usage/cost plumbing, transient errors, context-overflow detection, and model capability overrides for reasoning families. |
| Anthropic | `providers.Anthropic`, `providers.NewAnthropic` | Supported | Native Anthropic Messages adapter. Covers generation, streaming, tool use, thinking blocks, JSON-schema response format via forced tool use, usage/cost plumbing, transient errors, context-overflow detection, and known thinking-capable model overrides. |
| OpenRouter | `providers.OpenRouter`, `providers.NewOpenRouter` | Provisional | Uses the OpenAI-compatible adapter against OpenRouter's endpoint. Hosts should provide model metadata and verify model-specific tool, streaming, and reasoning behavior for their selected models. |
| Gemini | `providers.Gemini`, `providers.NewGemini` | Provisional | Uses Gemini's OpenAI-compatible endpoint. Hosts should validate the selected model and supply capability overrides when Gemini behavior differs from OpenAI-compatible defaults. |
| Ollama | `providers.Ollama`, `providers.NewOllama` | Provisional | Uses Ollama's local OpenAI-compatible endpoint. Good for local development, but model support varies by installed model and Ollama version. |
| Custom OpenAI-compatible endpoint | `providers.NewOpenAICompatible` | Bring-your-own | Use for compatible APIs not listed above. The caller owns provider ID, endpoint, headers, model list, cost metadata, capability overrides, and live compatibility validation. |
| Other providers | N/A | Deferred | Add an adapter when a real consumer needs it and the provider can satisfy the shared `llm.Provider` contract without host-specific policy. |

## M1 Requirements

Before first alpha:

- README and examples must not imply broader provider support than this matrix.
- OpenAI and Anthropic provider paths must keep their local unit coverage green.
- Provisional providers must build and pass shared provider tests, but they do
  not need live integration tests for M1.
- Consumer validation may choose any provider, but Canto's alpha contract is
  only the matrix above. If a host needs stronger guarantees for a provisional
  provider, that becomes a Canto task or an explicit host-side acceptance risk.

## Configuration Notes

Built-in providers read API keys from their standard environment variables when
not supplied explicitly:

| Provider | Environment variables |
| :--- | :--- |
| OpenAI | `OPENAI_API_KEY` |
| Anthropic | `ANTHROPIC_API_KEY` |
| OpenRouter | `OPENROUTER_API_KEY` |
| Gemini | `GEMINI_API_KEY`, `GOOGLE_API_KEY` |
| Ollama | `OLLAMA_API_KEY` optional; defaults to local Ollama-compatible auth placeholder |

For production hosts, pass explicit `providers.Config` values when endpoint,
headers, model metadata, or cost accounting need to be controlled by the host.

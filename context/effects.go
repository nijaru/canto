package context

// ProcessorEffects describes whether a processor has side effects beyond
// rewriting the in-flight LLM request.
type ProcessorEffects struct {
	// Session indicates the processor appends durable facts to the session log
	// or otherwise mutates session-owned state.
	Session bool
	// External indicates the processor writes external artifacts such as files
	// or other out-of-process state.
	External bool
}

// HasSideEffects reports whether the processor mutates session or external
// state in addition to the request.
func (e ProcessorEffects) HasSideEffects() bool {
	return e.Session || e.External
}

func (e ProcessorEffects) merge(other ProcessorEffects) ProcessorEffects {
	return ProcessorEffects{
		Session:  e.Session || other.Session,
		External: e.External || other.External,
	}
}

// EffectDescriber is implemented by processors that expose their side-effect
// characteristics.
type EffectDescriber interface {
	Effects() ProcessorEffects
}

// EffectsOf returns the declared effects of a processor. Processors that do
// not implement EffectDescriber are treated as request-only transforms.
func EffectsOf(p Processor) ProcessorEffects {
	if p == nil {
		return ProcessorEffects{}
	}
	if d, ok := p.(EffectDescriber); ok {
		return d.Effects()
	}
	return ProcessorEffects{}
}

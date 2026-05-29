package artifact

import (
	"context"
	"io"

	"github.com/nijaru/ion/session"
)

// Descriptor identifies a durable artifact emitted by a session, tool, or run.
// Descriptors are safe to store in session events because they contain stable
// references and provenance, not artifact bodies.
type Descriptor = session.ArtifactRef

// Store persists artifact bodies behind stable descriptors.
type Store interface {
	Put(ctx context.Context, desc Descriptor, r io.Reader) (Descriptor, error)
	Stat(ctx context.Context, id string) (Descriptor, error)
	Open(ctx context.Context, id string) (io.ReadCloser, Descriptor, error)
}

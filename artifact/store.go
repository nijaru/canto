package artifact

import (
	"context"
	"io"
)

// Descriptor identifies a durable artifact emitted by a session, tool, or run.
// Descriptors are safe to store in session events because they contain stable
// references and provenance, not artifact bodies.
type Descriptor struct {
	ID                string         `json:"id"`
	Kind              string         `json:"kind"`
	URI               string         `json:"uri"`
	Label             string         `json:"label,omitzero"`
	MIMEType          string         `json:"mime_type,omitzero"`
	Size              int64          `json:"size,omitzero"`
	Digest            string         `json:"digest,omitzero"`
	ProducerSessionID string         `json:"producer_session_id,omitzero"`
	ProducerEventID   string         `json:"producer_event_id,omitzero"`
	Metadata          map[string]any `json:"metadata,omitzero"`
}

// Store persists artifact bodies behind stable descriptors.
type Store interface {
	Put(ctx context.Context, desc Descriptor, r io.Reader) (Descriptor, error)
	Stat(ctx context.Context, id string) (Descriptor, error)
	Open(ctx context.Context, id string) (io.ReadCloser, Descriptor, error)
}

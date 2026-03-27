package session

import (
	"context"
	"fmt"
	"io"

	"github.com/nijaru/canto/artifact"
)

// RecordArtifact appends an artifact_recorded event for an existing durable
// artifact descriptor or external artifact reference.
func RecordArtifact(
	ctx context.Context,
	sess *Session,
	data ArtifactRecordedData,
) error {
	if sess == nil {
		return fmt.Errorf("record artifact: nil session")
	}

	data.Artifact = withDefaultArtifactProvenance(data.Artifact, sess.ID())
	return sess.Append(ctx, NewArtifactRecordedEvent(sess.ID(), data))
}

// StoreArtifact persists an artifact body behind store, fills the common
// provenance defaults, and records the resulting descriptor in the session.
func StoreArtifact(
	ctx context.Context,
	sess *Session,
	store artifact.Store,
	data ArtifactRecordedData,
	body io.Reader,
) (ArtifactRef, error) {
	if sess == nil {
		return ArtifactRef{}, fmt.Errorf("store artifact: nil session")
	}
	if store == nil {
		return ArtifactRef{}, fmt.Errorf("store artifact: nil store")
	}
	if body == nil {
		return ArtifactRef{}, fmt.Errorf("store artifact: nil body")
	}

	desc := withDefaultArtifactProvenance(data.Artifact, sess.ID())
	desc, err := store.Put(ctx, desc, body)
	if err != nil {
		return ArtifactRef{}, fmt.Errorf("store artifact: %w", err)
	}

	data.Artifact = desc
	if err := sess.Append(ctx, NewArtifactRecordedEvent(sess.ID(), data)); err != nil {
		return ArtifactRef{}, fmt.Errorf("store artifact record: %w", err)
	}
	return desc, nil
}

func withDefaultArtifactProvenance(
	desc artifact.Descriptor,
	sessionID string,
) artifact.Descriptor {
	if desc.ProducerSessionID == "" {
		desc.ProducerSessionID = sessionID
	}
	return desc
}

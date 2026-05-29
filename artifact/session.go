package artifact

import (
	"context"
	"fmt"
	"io"

	"github.com/nijaru/ion/session"
)

// StoreSessionArtifact persists an artifact body behind store and records the
// resulting descriptor in sess.
func StoreSessionArtifact(
	ctx context.Context,
	sess *session.Session,
	store Store,
	data session.ArtifactRecordedData,
	body io.Reader,
) (session.ArtifactRef, error) {
	if sess == nil {
		return session.ArtifactRef{}, fmt.Errorf("store artifact: nil session")
	}
	if store == nil {
		return session.ArtifactRef{}, fmt.Errorf("store artifact: nil store")
	}
	if body == nil {
		return session.ArtifactRef{}, fmt.Errorf("store artifact: nil body")
	}

	desc := data.Artifact
	if desc.ProducerSessionID == "" {
		desc.ProducerSessionID = sess.ID()
	}
	desc, err := store.Put(ctx, desc, body)
	if err != nil {
		return session.ArtifactRef{}, fmt.Errorf("store artifact: %w", err)
	}

	data.Artifact = desc
	if err := session.RecordArtifact(ctx, sess, data); err != nil {
		return session.ArtifactRef{}, fmt.Errorf("store artifact record: %w", err)
	}
	return desc, nil
}

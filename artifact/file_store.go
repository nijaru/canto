package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/go-json-experiment/json"
	"github.com/oklog/ulid/v2"
)

const (
	fileStoreDirPerm  = 0o755
	fileStoreFilePerm = 0o644
)

// FileStore stores artifacts under a local filesystem root.
type FileStore struct {
	root *os.Root
}

// NewFileStore creates a file-backed artifact store rooted at dir.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, fileStoreDirPerm); err != nil {
		return nil, fmt.Errorf("artifact file store mkdir %q: %w", dir, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("artifact file store open root %q: %w", dir, err)
	}
	return &FileStore{root: root}, nil
}

// Close releases the underlying filesystem root.
func (s *FileStore) Close() error {
	if s == nil || s.root == nil {
		return nil
	}
	return s.root.Close()
}

// Put stores an artifact body and returns the finalized descriptor.
func (s *FileStore) Put(ctx context.Context, desc Descriptor, r io.Reader) (Descriptor, error) {
	if err := ctx.Err(); err != nil {
		return Descriptor{}, err
	}
	if s == nil || s.root == nil {
		return Descriptor{}, fmt.Errorf("artifact file store: nil root")
	}

	body, err := io.ReadAll(r)
	if err != nil {
		return Descriptor{}, fmt.Errorf("artifact file store read body: %w", err)
	}

	desc = finalizeDescriptor(desc, body)
	if err := s.root.MkdirAll(descriptorDir(desc.ID), fileStoreDirPerm); err != nil {
		return Descriptor{}, fmt.Errorf("artifact file store mkdir artifact %q: %w", desc.ID, err)
	}

	desc.URI = fileURI(filepath.Join(s.root.Name(), descriptorBodyPath(desc.ID)))
	raw, err := json.Marshal(desc)
	if err != nil {
		return Descriptor{}, fmt.Errorf(
			"artifact file store marshal descriptor %q: %w",
			desc.ID,
			err,
		)
	}
	if err := s.root.WriteFile(descriptorBodyPath(desc.ID), body, fileStoreFilePerm); err != nil {
		return Descriptor{}, fmt.Errorf("artifact file store write body %q: %w", desc.ID, err)
	}
	if err := s.root.WriteFile(descriptorMetadataPath(desc.ID), raw, fileStoreFilePerm); err != nil {
		return Descriptor{}, fmt.Errorf("artifact file store write descriptor %q: %w", desc.ID, err)
	}
	return desc, nil
}

// Stat returns the stored descriptor for an artifact.
func (s *FileStore) Stat(ctx context.Context, id string) (Descriptor, error) {
	if err := ctx.Err(); err != nil {
		return Descriptor{}, err
	}
	if s == nil || s.root == nil {
		return Descriptor{}, fmt.Errorf("artifact file store: nil root")
	}

	raw, err := s.root.ReadFile(descriptorMetadataPath(id))
	if err != nil {
		return Descriptor{}, fmt.Errorf("artifact file store read descriptor %q: %w", id, err)
	}
	var desc Descriptor
	if err := json.Unmarshal(raw, &desc); err != nil {
		return Descriptor{}, fmt.Errorf("artifact file store decode descriptor %q: %w", id, err)
	}
	return desc, nil
}

// Open opens an artifact body and returns its descriptor.
func (s *FileStore) Open(ctx context.Context, id string) (io.ReadCloser, Descriptor, error) {
	if err := ctx.Err(); err != nil {
		return nil, Descriptor{}, err
	}
	desc, err := s.Stat(ctx, id)
	if err != nil {
		return nil, Descriptor{}, err
	}
	f, err := s.root.Open(descriptorBodyPath(id))
	if err != nil {
		return nil, Descriptor{}, fmt.Errorf("artifact file store open body %q: %w", id, err)
	}
	return f, desc, nil
}

func finalizeDescriptor(desc Descriptor, body []byte) Descriptor {
	if desc.ID == "" {
		desc.ID = ulid.Make().String()
	}
	if desc.Size == 0 {
		desc.Size = int64(len(body))
	}
	if desc.Digest == "" {
		sum := sha256.Sum256(body)
		desc.Digest = "sha256:" + hex.EncodeToString(sum[:])
	}
	if desc.Metadata == nil {
		desc.Metadata = make(map[string]any)
	}
	return desc
}

func descriptorDir(id string) string {
	return filepath.Join("objects", id)
}

func descriptorBodyPath(id string) string {
	return filepath.Join(descriptorDir(id), "body")
}

func descriptorMetadataPath(id string) string {
	return filepath.Join(descriptorDir(id), "descriptor.json")
}

func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

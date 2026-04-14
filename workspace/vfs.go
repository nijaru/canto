package workspace

import (
	"context"
	"io/fs"
	"os"
	"path"
	"strings"
)

// WorkspaceFS is the rooted filesystem capability exposed to agents and hosts.
//
// Root implements this interface today. Later overlay and snapshot layers can
// satisfy the same contract without changing the callers that only need
// workspace-backed reads and writes.
type WorkspaceFS interface {
	Path() string
	Close() error
	FS() fs.FS
	MkdirAll(path string, perm os.FileMode) error
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	ReadDir(name string) ([]fs.DirEntry, error)
	Stat(name string) (fs.FileInfo, error)
	Glob(ctx context.Context, pattern string) ([]string, error)
}

// MultiFS allows mounting multiple WorkspaceFS implementations at different
// rooted paths.
type MultiFS struct {
	base   WorkspaceFS
	mounts map[string]WorkspaceFS
}

// NewMultiFS creates a new MultiFS wrapping a base filesystem.
func NewMultiFS(base WorkspaceFS) *MultiFS {
	return &MultiFS{
		base:   base,
		mounts: make(map[string]WorkspaceFS),
	}
}

// Mount attaches a filesystem at the given rooted path.
func (m *MultiFS) Mount(path string, fs WorkspaceFS) {
	path = strings.Trim(path, "/")
	if path == "" {
		m.base = fs
		return
	}
	m.mounts[path] = fs
}

func (m *MultiFS) Path() string {
	return m.base.Path()
}

func (m *MultiFS) Close() error {
	err := m.base.Close()
	for _, fs := range m.mounts {
		if closeErr := fs.Close(); closeErr != nil {
			err = closeErr
		}
	}
	return err
}

func (m *MultiFS) FS() fs.FS {
	// Simple delegation to base for now, ideally returns a combined FS.
	return m.base.FS()
}

func (m *MultiFS) MkdirAll(name string, perm os.FileMode) error {
	fs, sub, ok := m.resolve(name)
	if ok {
		return fs.MkdirAll(sub, perm)
	}
	return m.base.MkdirAll(name, perm)
}

func (m *MultiFS) ReadFile(name string) ([]byte, error) {
	fs, sub, ok := m.resolve(name)
	if ok {
		return fs.ReadFile(sub)
	}
	return m.base.ReadFile(name)
}

func (m *MultiFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	fs, sub, ok := m.resolve(name)
	if ok {
		return fs.WriteFile(sub, data, perm)
	}
	return m.base.WriteFile(name, data, perm)
}

func (m *MultiFS) ReadDir(name string) ([]fs.DirEntry, error) {
	fs, sub, ok := m.resolve(name)
	if ok {
		return fs.ReadDir(sub)
	}
	// If name is root, we should also include mount points.
	entries, err := m.base.ReadDir(name)
	if name == "." || name == "" {
		for mount := range m.mounts {
			entries = append(entries, virtualDirEntry{name: mount})
		}
	}
	return entries, err
}

func (m *MultiFS) Stat(name string) (fs.FileInfo, error) {
	fs, sub, ok := m.resolve(name)
	if ok {
		return fs.Stat(sub)
	}
	return m.base.Stat(name)
}

func (m *MultiFS) Glob(ctx context.Context, pattern string) ([]string, error) {
	// For now, only search base.
	return m.base.Glob(ctx, pattern)
}

func (m *MultiFS) resolve(name string) (WorkspaceFS, string, bool) {
	name = strings.TrimPrefix(path.Clean(name), "/")
	for mount, fs := range m.mounts {
		if name == mount {
			return fs, ".", true
		}
		if strings.HasPrefix(name, mount+"/") {
			return fs, strings.TrimPrefix(name, mount+"/"), true
		}
	}
	return nil, "", false
}

type virtualDirEntry struct {
	name string
}

func (e virtualDirEntry) Name() string               { return e.name }
func (e virtualDirEntry) IsDir() bool                { return true }
func (e virtualDirEntry) Type() fs.FileMode          { return os.ModeDir }
func (e virtualDirEntry) Info() (fs.FileInfo, error) { return nil, nil }

var _ WorkspaceFS = (*Root)(nil)

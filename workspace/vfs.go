package workspace

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
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
	Remove(name string) error
	ReadDir(name string) ([]fs.DirEntry, error)
	Stat(name string) (fs.FileInfo, error)
	Glob(ctx context.Context, pattern string) ([]string, error)
}

// OverlayFS implements a speculative virtual filesystem layer over a base
// workspace. Writes are buffered in memory and can be committed or discarded.
type OverlayFS struct {
	mu          sync.RWMutex
	base        WorkspaceFS
	speculative map[string]*overlayFile
	deleted     map[string]struct{}
}

type overlayFile struct {
	name    string
	data    []byte
	perm    os.FileMode
	modTime time.Time
	isDir   bool
}

// NewOverlayFS creates a new speculative overlay over a base filesystem.
func NewOverlayFS(base WorkspaceFS) *OverlayFS {
	return &OverlayFS{
		base:        base,
		speculative: make(map[string]*overlayFile),
		deleted:     make(map[string]struct{}),
	}
}

func (o *OverlayFS) Path() string { return o.base.Path() }
func (o *OverlayFS) Close() error { return o.base.Close() }
func (o *OverlayFS) FS() fs.FS    { return o.base.FS() } // Ideally returns a merged fs.FS

func (o *OverlayFS) ensureParents(name string) {
	dir := path.Dir(name)
	for dir != "." && dir != "/" {
		if _, ok := o.speculative[dir]; !ok {
			o.speculative[dir] = &overlayFile{
				name:    path.Base(dir),
				perm:    0o755,
				modTime: time.Now(),
				isDir:   true,
			}
			delete(o.deleted, dir)
		}
		dir = path.Dir(dir)
	}
}

func (o *OverlayFS) MkdirAll(name string, perm os.FileMode) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	name = path.Clean(name)
	o.ensureParents(name)
	o.speculative[name] = &overlayFile{
		name:    path.Base(name),
		perm:    perm,
		modTime: time.Now(),
		isDir:   true,
	}
	delete(o.deleted, name)
	return nil
}

func (o *OverlayFS) ReadFile(name string) ([]byte, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	name = path.Clean(name)
	if _, ok := o.deleted[name]; ok {
		return nil, os.ErrNotExist
	}
	if f, ok := o.speculative[name]; ok {
		if f.isDir {
			return nil, fmt.Errorf("read: %s is a directory", name)
		}
		return slices.Clone(f.data), nil
	}
	return o.base.ReadFile(name)
}

func (o *OverlayFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	name = path.Clean(name)
	o.ensureParents(name)
	o.speculative[name] = &overlayFile{
		name:    path.Base(name),
		data:    slices.Clone(data),
		perm:    perm,
		modTime: time.Now(),
		isDir:   false,
	}
	delete(o.deleted, name)
	return nil
}

func (o *OverlayFS) Remove(name string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	name = path.Clean(name)
	o.deleted[name] = struct{}{}
	delete(o.speculative, name)
	return nil
}

func (o *OverlayFS) ReadDir(name string) ([]fs.DirEntry, error) {
	name = path.Clean(name)

	o.mu.RLock()
	baseEntries, err := o.base.ReadDir(name)
	if err != nil && !os.IsNotExist(err) {
		o.mu.RUnlock()
		return nil, err
	}
	spec := make(map[string]*overlayFile, len(o.speculative))
	for key, value := range o.speculative {
		spec[key] = value
	}
	deleted := make(map[string]struct{}, len(o.deleted))
	for key := range o.deleted {
		deleted[key] = struct{}{}
	}
	o.mu.RUnlock()

	entryMap := make(map[string]fs.DirEntry)
	for _, e := range baseEntries {
		p := path.Join(name, e.Name())
		if _, ok := deleted[p]; !ok {
			entryMap[e.Name()] = e
		}
	}

	for p, f := range spec {
		if path.Dir(p) == name {
			entryMap[f.name] = &overlayDirEntry{f: f}
		}
	}

	entries := make([]fs.DirEntry, 0, len(entryMap))
	for _, e := range entryMap {
		entries = append(entries, e)
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})
	return entries, nil
}

func (o *OverlayFS) Stat(name string) (fs.FileInfo, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	name = path.Clean(name)
	if _, ok := o.deleted[name]; ok {
		return nil, os.ErrNotExist
	}
	if f, ok := o.speculative[name]; ok {
		return &overlayFileInfo{f: f}, nil
	}
	return o.base.Stat(name)
}

func (o *OverlayFS) Glob(ctx context.Context, pattern string) ([]string, error) {
	baseMatches, err := o.base.Glob(ctx, pattern)
	if err != nil {
		return nil, err
	}

	o.mu.RLock()
	spec := make(map[string]*overlayFile, len(o.speculative))
	for key, value := range o.speculative {
		spec[key] = value
	}
	deleted := make(map[string]struct{}, len(o.deleted))
	for key := range o.deleted {
		deleted[key] = struct{}{}
	}
	o.mu.RUnlock()

	matchSet := make(map[string]struct{}, len(baseMatches)+len(spec))
	for _, match := range baseMatches {
		if _, ok := deleted[path.Clean(match)]; !ok {
			matchSet[match] = struct{}{}
		}
	}
	for name := range spec {
		if _, ok := deleted[name]; ok {
			continue
		}
		matched, err := path.Match(pattern, name)
		if err != nil {
			return nil, err
		}
		if matched {
			matchSet[name] = struct{}{}
		}
	}

	matches := make([]string, 0, len(matchSet))
	for match := range matchSet {
		matches = append(matches, match)
	}
	slices.Sort(matches)
	return matches, nil
}

// Commit applies all speculative changes to the base filesystem.
func (o *OverlayFS) Commit(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	// 1. Remove (children before parents)
	deletedPaths := make([]string, 0, len(o.deleted))
	for p := range o.deleted {
		deletedPaths = append(deletedPaths, p)
	}
	slices.SortFunc(deletedPaths, func(a, b string) int {
		return strings.Compare(
			b,
			a,
		) // descending order by length typically, or reverse alphabetical
	})
	for _, p := range deletedPaths {
		if err := o.base.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	// 2. MkdirAll & WriteFile (parents before children)
	specPaths := make([]string, 0, len(o.speculative))
	for p := range o.speculative {
		specPaths = append(specPaths, p)
	}
	slices.SortFunc(specPaths, strings.Compare)

	for _, p := range specPaths {
		f := o.speculative[p]
		if f.isDir {
			if err := o.base.MkdirAll(p, f.perm); err != nil {
				return err
			}
		} else {
			if err := o.base.WriteFile(p, f.data, f.perm); err != nil {
				return err
			}
		}
	}

	o.speculative = make(map[string]*overlayFile)
	o.deleted = make(map[string]struct{})
	return nil
}

// Discard clears all speculative changes.
func (o *OverlayFS) Discard() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.speculative = make(map[string]*overlayFile)
	o.deleted = make(map[string]struct{})
}

// OverlaySnapshot is a point-in-time capture of the speculative overlay state.
type OverlaySnapshot struct {
	speculative map[string]*overlayFile
	deleted     map[string]struct{}
}

// Snapshot captures the current speculative state.
func (o *OverlayFS) Snapshot() *OverlaySnapshot {
	o.mu.RLock()
	defer o.mu.RUnlock()

	spec := make(map[string]*overlayFile, len(o.speculative))
	for k, v := range o.speculative {
		spec[k] = v // overlayFile is immutable after creation
	}
	deleted := make(map[string]struct{}, len(o.deleted))
	for k := range o.deleted {
		deleted[k] = struct{}{}
	}
	return &OverlaySnapshot{speculative: spec, deleted: deleted}
}

// RestoreSnapshot replaces the current speculative state with a previous snapshot.
func (o *OverlayFS) RestoreSnapshot(s *OverlaySnapshot) {
	if s == nil {
		o.Discard()
		return
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	spec := make(map[string]*overlayFile, len(s.speculative))
	for k, v := range s.speculative {
		spec[k] = v
	}
	deleted := make(map[string]struct{}, len(s.deleted))
	for k := range s.deleted {
		deleted[k] = struct{}{}
	}
	o.speculative = spec
	o.deleted = deleted
}

type overlayDirEntry struct {
	f *overlayFile
}

func (e *overlayDirEntry) Name() string               { return e.f.name }
func (e *overlayDirEntry) IsDir() bool                { return e.f.isDir }
func (e *overlayDirEntry) Type() fs.FileMode          { return e.f.perm.Type() }
func (e *overlayDirEntry) Info() (fs.FileInfo, error) { return &overlayFileInfo{f: e.f}, nil }

type overlayFileInfo struct {
	f *overlayFile
}

func (i *overlayFileInfo) Name() string       { return i.f.name }
func (i *overlayFileInfo) Size() int64        { return int64(len(i.f.data)) }
func (i *overlayFileInfo) Mode() os.FileMode  { return i.f.perm }
func (i *overlayFileInfo) ModTime() time.Time { return i.f.modTime }
func (i *overlayFileInfo) IsDir() bool        { return i.f.isDir }
func (i *overlayFileInfo) Sys() any           { return nil }

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

func (m *MultiFS) Remove(name string) error {
	fs, sub, ok := m.resolve(name)
	if ok {
		return fs.Remove(sub)
	}
	return m.base.Remove(name)
}

func (m *MultiFS) ReadDir(name string) ([]fs.DirEntry, error) {
	mounted, sub, ok := m.resolve(name)
	if ok {
		return mounted.ReadDir(sub)
	}
	entries, err := m.base.ReadDir(name)
	if name == "." || name == "" {
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		for mount := range m.mounts {
			entries = append(entries, virtualDirEntry{name: mount})
		}
		slices.SortFunc(entries, func(a, b fs.DirEntry) int {
			return strings.Compare(a.Name(), b.Name())
		})
		return entries, nil
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
	pattern = strings.TrimPrefix(path.Clean(pattern), "/")
	baseMatches, err := m.base.Glob(ctx, pattern)
	if err != nil {
		return nil, err
	}

	matchSet := make(map[string]struct{}, len(baseMatches)+len(m.mounts))
	for _, match := range baseMatches {
		matchSet[match] = struct{}{}
	}
	for mount, mounted := range m.mounts {
		matched, err := path.Match(pattern, mount)
		if err != nil {
			return nil, err
		}
		if matched {
			matchSet[mount] = struct{}{}
		}
		if !strings.HasPrefix(pattern, mount+"/") {
			continue
		}
		subPattern := strings.TrimPrefix(pattern, mount+"/")
		mountedMatches, err := mounted.Glob(ctx, subPattern)
		if err != nil {
			return nil, err
		}
		for _, match := range mountedMatches {
			matchSet[path.Join(mount, match)] = struct{}{}
		}
	}

	matches := make([]string, 0, len(matchSet))
	for match := range matchSet {
		matches = append(matches, match)
	}
	slices.Sort(matches)
	return matches, nil
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
func (e virtualDirEntry) Info() (fs.FileInfo, error) { return virtualFileInfo{name: e.name}, nil }

type virtualFileInfo struct {
	name string
}

func (i virtualFileInfo) Name() string       { return i.name }
func (i virtualFileInfo) Size() int64        { return 0 }
func (i virtualFileInfo) Mode() os.FileMode  { return os.ModeDir }
func (i virtualFileInfo) ModTime() time.Time { return time.Time{} }
func (i virtualFileInfo) IsDir() bool        { return true }
func (i virtualFileInfo) Sys() any           { return nil }

var _ WorkspaceFS = (*Root)(nil)

type contextKey struct{}

// WithContext returns a new context that carries a WorkspaceFS.
func WithContext(ctx context.Context, fs WorkspaceFS) context.Context {
	return context.WithValue(ctx, contextKey{}, fs)
}

// FromContext returns the WorkspaceFS associated with the context, if any.
func FromContext(ctx context.Context) (WorkspaceFS, bool) {
	fs, ok := ctx.Value(contextKey{}).(WorkspaceFS)
	return fs, ok
}

package memory

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nijaru/canto/workspace"
)

// FS exposes a memory Manager as a read-only workspace.WorkspaceFS.
// This allows agents to use native file tools (cat, grep, ls) over durable
// memory blocks and vector chunks.
//
// Layout:
// /blocks/<scope>/<scope_id>/<name>.md
// /memories/<scope>/<scope_id>/<role>/<id>.md
// /docs/<path> - Reassembles memories with metadata["path"] == path.
// /search/<query>.md - Virtual search result file.
type FS struct {
	manager *Manager
}

var _ workspace.WorkspaceFS = (*FS)(nil)

// NewFS creates a new virtual filesystem exposing the memory manager.
func NewFS(manager *Manager) *FS {
	return &FS{manager: manager}
}

func (f *FS) Path() string {
	return "memory://"
}

func (f *FS) Close() error {
	return nil
}

func (f *FS) FS() fs.FS {
	return virtualFS{f}
}

func (f *FS) MkdirAll(path string, perm os.FileMode) error {
	return os.ErrPermission
}

func (f *FS) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.ErrPermission
}

func (f *FS) Remove(name string) error {
	return os.ErrPermission
}

func (f *FS) ReadFile(name string) ([]byte, error) {
	name = path.Clean(strings.TrimPrefix(name, "/"))
	parts := strings.Split(name, "/")

	if len(parts) >= 4 && parts[0] == "blocks" {
		scope := parts[1]
		scopeID := parts[2]
		blockName := strings.TrimSuffix(strings.Join(parts[3:], "/"), ".md")

		ns := Namespace{Scope: Scope(scope), ID: scopeID}
		block, err := f.manager.store.GetBlock(context.Background(), ns, blockName)
		if err != nil {
			return nil, err
		}
		if block == nil {
			return nil, fs.ErrNotExist
		}
		return []byte(block.Content), nil
	}

	if len(parts) == 5 && parts[0] == "memories" {
		id := strings.TrimSuffix(parts[4], ".md")

		mem, err := f.manager.store.GetMemory(context.Background(), id)
		if err != nil {
			return nil, err
		}
		if mem == nil {
			return nil, fs.ErrNotExist
		}
		return []byte(mem.Content), nil
	}

	if len(parts) >= 2 && parts[0] == "docs" {
		docPath := strings.Join(parts[1:], "/")
		q := Query{
			IncludeRecent: true,
			Filters:       map[string]any{"path": docPath},
			Limit:         100, // Reassemble up to 100 chunks
		}
		results, err := f.manager.Retrieve(context.Background(), q)
		if err != nil {
			return nil, err
		}
		if len(results) == 0 {
			return nil, fs.ErrNotExist
		}

		// Sort by chunk index if present in metadata
		slices.SortFunc(results, func(a, b Memory) int {
			ca := getChunk(a.Metadata)
			cb := getChunk(b.Metadata)
			if ca != cb {
				return ca - cb
			}
			return strings.Compare(a.ID, b.ID)
		})

		var out strings.Builder
		for _, res := range results {
			out.WriteString(res.Content)
		}
		return []byte(out.String()), nil
	}

	if len(parts) == 2 && parts[0] == "search" {
		queryText := strings.TrimSuffix(parts[1], ".md")
		q := Query{
			Text:        queryText,
			Limit:       10,
			UseSemantic: true,
		}
		results, err := f.manager.Retrieve(context.Background(), q)
		if err != nil {
			return nil, err
		}
		var out strings.Builder
		for i, hit := range results {
			fmt.Fprintf(
				&out,
				"## Match %d (ID: %s, Score: %.2f)\n\n%s\n\n",
				i+1,
				hit.ID,
				hit.Score,
				hit.Content,
			)
		}
		return []byte(out.String()), nil
	}

	return nil, fs.ErrNotExist
}

func getChunk(m map[string]any) int {
	if m == nil {
		return 0
	}
	v, ok := m["chunk"]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case int64:
		return int(val)
	case float64:
		return int(val)
	case string:
		i, _ := strconv.Atoi(val)
		return i
	default:
		return 0
	}
}

func (f *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	name = path.Clean(strings.TrimPrefix(name, "/"))
	if name == "." || name == "" {
		return []fs.DirEntry{
			virtualDirEntry{name: "blocks"},
			virtualDirEntry{name: "memories"},
			virtualDirEntry{name: "docs"},
			virtualDirEntry{name: "search"},
		}, nil
	}

	if name == "docs" {
		// This would be expensive to list all unique paths in a real store.
		// For exploration, we could list a few or just return empty.
		return nil, nil
	}

	return nil, fs.ErrNotExist
}

func (f *FS) Stat(name string) (fs.FileInfo, error) {
	name = path.Clean(strings.TrimPrefix(name, "/"))
	if name == "." || name == "" || name == "blocks" || name == "memories" || name == "docs" ||
		name == "search" {
		return virtualDirInfo{name: name}, nil
	}

	data, err := f.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return virtualFileInfo{name: path.Base(name), size: int64(len(data))}, nil
}

func (f *FS) Glob(ctx context.Context, pattern string) ([]string, error) {
	// Glob supports wildcarding. If we grep -r memory:// we might want to list some docs.
	return nil, nil
}

type virtualFS struct {
	f *FS
}

func (v virtualFS) Open(name string) (fs.File, error) {
	info, err := v.f.Stat(name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return &virtualDirFile{info: info}, nil
	}
	data, err := v.f.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return &virtualFile{info: info, data: data}, nil
}

type virtualFile struct {
	info   fs.FileInfo
	data   []byte
	offset int64
}

func (f *virtualFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *virtualFile) Read(b []byte) (int, error) {
	if f.offset >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(b, f.data[f.offset:])
	f.offset += int64(n)
	return n, nil
}
func (f *virtualFile) Close() error { return nil }

type virtualDirFile struct {
	info fs.FileInfo
}

func (d *virtualDirFile) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *virtualDirFile) Read(b []byte) (int, error) { return 0, fmt.Errorf("is a directory") }
func (d *virtualDirFile) Close() error               { return nil }

type virtualFileInfo struct {
	name string
	size int64
}

func (i virtualFileInfo) Name() string       { return i.name }
func (i virtualFileInfo) Size() int64        { return i.size }
func (i virtualFileInfo) Mode() os.FileMode  { return 0o444 }
func (i virtualFileInfo) ModTime() time.Time { return time.Time{} }
func (i virtualFileInfo) IsDir() bool        { return false }
func (i virtualFileInfo) Sys() any           { return nil }

type virtualDirInfo struct {
	name string
}

func (i virtualDirInfo) Name() string       { return path.Base(i.name) }
func (i virtualDirInfo) Size() int64        { return 0 }
func (i virtualDirInfo) Mode() os.FileMode  { return os.ModeDir | 0o555 }
func (i virtualDirInfo) ModTime() time.Time { return time.Time{} }
func (i virtualDirInfo) IsDir() bool        { return true }
func (i virtualDirInfo) Sys() any           { return nil }

type virtualDirEntry struct {
	name string
}

func (e virtualDirEntry) Name() string               { return e.name }
func (e virtualDirEntry) IsDir() bool                { return true }
func (e virtualDirEntry) Type() fs.FileMode          { return os.ModeDir }
func (e virtualDirEntry) Info() (fs.FileInfo, error) { return virtualDirInfo{name: e.name}, nil }

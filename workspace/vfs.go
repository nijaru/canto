package workspace

import (
	"context"
	"io/fs"
	"os"
)

// VirtualFS is the rooted filesystem capability exposed to agents and hosts.
//
// Root implements this interface today. Later overlay and snapshot layers can
// satisfy the same contract without changing the callers that only need
// workspace-backed reads and writes.
type VirtualFS interface {
	Path() string
	Close() error
	FS() fs.FS
	Open(name string) (*os.File, error)
	OpenFile(name string, flag int, perm os.FileMode) (*os.File, error)
	MkdirAll(path string, perm os.FileMode) error
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	ReadDir(name string) ([]fs.DirEntry, error)
	Stat(name string) (fs.FileInfo, error)
	Glob(ctx context.Context, pattern string) ([]string, error)
}

var _ VirtualFS = (*Root)(nil)

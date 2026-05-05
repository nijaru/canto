package memory

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"time"
)

type virtualFS struct {
	f *FS
}

func (v virtualFS) Open(name string) (fs.File, error) {
	info, err := v.f.Stat(name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		entries, err := v.f.ReadDir(name)
		if err != nil {
			return nil, err
		}
		return &virtualDirFile{info: info, entries: entries}, nil
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
	info    fs.FileInfo
	entries []fs.DirEntry
	offset  int
}

func (d *virtualDirFile) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *virtualDirFile) Read(b []byte) (int, error) { return 0, fmt.Errorf("is a directory") }
func (d *virtualDirFile) Close() error               { return nil }

func (d *virtualDirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	if d.offset >= len(d.entries) {
		if n <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	if n <= 0 {
		entries := append([]fs.DirEntry(nil), d.entries[d.offset:]...)
		d.offset = len(d.entries)
		return entries, nil
	}
	end := min(d.offset+n, len(d.entries))
	entries := append([]fs.DirEntry(nil), d.entries[d.offset:end]...)
	d.offset = end
	return entries, nil
}

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

package workspace

import (
	"os"
	"testing"
)

type mockFS struct {
	WorkspaceFS
	data map[string]string
}

func (m *mockFS) ReadFile(name string) ([]byte, error) {
	if d, ok := m.data[name]; ok {
		return []byte(d), nil
	}
	return nil, os.ErrNotExist
}

func (m *mockFS) Path() string { return "mock://" }
func (m *mockFS) Close() error { return nil }

func TestMultiFS(t *testing.T) {
	base := &mockFS{data: map[string]string{"base.txt": "base content"}}
	mount := &mockFS{data: map[string]string{"mount.txt": "mount content"}}

	multi := NewMultiFS(base)
	multi.Mount("memory", mount)

	// Read from base
	data, err := multi.ReadFile("base.txt")
	if err != nil {
		t.Errorf("ReadFile base.txt: %v", err)
	}
	if string(data) != "base content" {
		t.Errorf("wrong base content: %q", string(data))
	}

	// Read from mount
	data, err = multi.ReadFile("memory/mount.txt")
	if err != nil {
		t.Errorf("ReadFile memory/mount.txt: %v", err)
	}
	if string(data) != "mount content" {
		t.Errorf("wrong mount content: %q", string(data))
	}

	// Read non-existent from mount
	_, err = multi.ReadFile("memory/missing.txt")
	if !os.IsNotExist(err) {
		t.Errorf("expected NotExist for memory/missing.txt, got %v", err)
	}
}

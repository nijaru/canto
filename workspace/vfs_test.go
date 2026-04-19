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

func (m *mockFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	m.data[name] = string(data)
	return nil
}

func (m *mockFS) MkdirAll(path string, perm os.FileMode) error { return nil }
func (m *mockFS) Remove(name string) error {
	delete(m.data, name)
	return nil
}

func (m *mockFS) Path() string { return "mock://" }
func (m *mockFS) Close() error { return nil }

func TestOverlayFS(t *testing.T) {
	base := &mockFS{data: map[string]string{"base.txt": "base content"}}
	overlay := NewOverlayFS(base)

	// Read from base
	data, err := overlay.ReadFile("base.txt")
	if err != nil {
		t.Errorf("ReadFile base.txt: %v", err)
	}
	if string(data) != "base content" {
		t.Errorf("wrong content: %q", string(data))
	}

	// Write speculative
	if err := overlay.WriteFile("spec.txt", []byte("spec content"), 0o644); err != nil {
		t.Fatalf("WriteFile spec.txt: %v", err)
	}

	// Read speculative
	data, err = overlay.ReadFile("spec.txt")
	if err != nil {
		t.Errorf("ReadFile spec.txt: %v", err)
	}
	if string(data) != "spec content" {
		t.Errorf("wrong spec content: %q", string(data))
	}

	// Base remains untouched
	if _, ok := base.data["spec.txt"]; ok {
		t.Error("base should not have spec.txt yet")
	}

	// Test Snapshot
	snap := overlay.Snapshot()

	// Modify more
	if err := overlay.WriteFile("spec2.txt", []byte("spec2 content"), 0o644); err != nil {
		t.Fatalf("WriteFile spec2.txt: %v", err)
	}

	// Restore Snapshot
	overlay.RestoreSnapshot(snap)

	// spec2.txt should be gone
	if _, err := overlay.ReadFile("spec2.txt"); !os.IsNotExist(err) {
		t.Errorf("spec2.txt should not exist after restore, got error: %v", err)
	}
	// spec.txt should still exist
	if _, err := overlay.ReadFile("spec.txt"); err != nil {
		t.Errorf("spec.txt should still exist after restore: %v", err)
	}

	// Test Commit
	if err := overlay.Commit(t.Context()); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Base should now have spec.txt
	if base.data["spec.txt"] != "spec content" {
		t.Errorf("base.txt missing from base after commit, got %q", base.data["spec.txt"])
	}

	// Speculative should be empty
	if len(overlay.speculative) != 0 {
		t.Error("speculative should be empty after commit")
	}

	// Test Delete Speculative
	if err := overlay.Remove("base.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := overlay.ReadFile("base.txt"); !os.IsNotExist(err) {
		t.Errorf("base.txt should be deleted from overlay, got error: %v", err)
	}
	// But still in base until commit
	if _, ok := base.data["base.txt"]; !ok {
		t.Error("base.txt should still be in base until commit")
	}

	if err := overlay.Commit(t.Context()); err != nil {
		t.Fatalf("Commit delete: %v", err)
	}
	if _, ok := base.data["base.txt"]; ok {
		t.Error("base.txt should be removed from base after commit")
	}
}

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

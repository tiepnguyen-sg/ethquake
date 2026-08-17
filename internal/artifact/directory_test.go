package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDirectoryWritesExclusivePrivateArtifacts(t *testing.T) {
	root := t.TempDir()
	directory, err := Create(root, "control-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := directory.WriteJSON("manifest.json", map[string]string{"status": "complete"}); err != nil {
		t.Fatalf("WriteJSON() error = %v", err)
	}
	path := filepath.Join(directory.Path(), "manifest.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat artifact: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	if err := directory.WriteJSON("manifest.json", map[string]string{}); err == nil {
		t.Fatal("WriteJSON() overwrite error = nil")
	}
}

func TestCreateRejectsClusterBoundEnvironment(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	if _, err := Create(t.TempDir(), "fault-1"); err == nil || !strings.Contains(err.Error(), "inside Kubernetes") {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestCreateRejectsSymlinkRoot(t *testing.T) {
	parent := t.TempDir()
	realRoot := filepath.Join(parent, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	symlinkRoot := filepath.Join(parent, "link")
	if err := os.Symlink(realRoot, symlinkRoot); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := Create(symlinkRoot, "fault-1"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestDirectoryRejectsTraversal(t *testing.T) {
	directory, err := Create(t.TempDir(), "fault-1")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := directory.CreateFile("../secret"); err == nil {
		t.Fatal("CreateFile() traversal error = nil")
	}
}

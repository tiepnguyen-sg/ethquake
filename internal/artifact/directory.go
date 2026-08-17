// Package artifact owns safe off-cluster evidence storage.
package artifact

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var componentPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type Directory struct {
	path string
}

func Create(root, runID string) (*Directory, error) {
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return nil, errors.New("canonical artifact creation from inside Kubernetes is prohibited")
	}
	if !componentPattern.MatchString(runID) {
		return nil, fmt.Errorf("run ID %q is invalid", runID)
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve artifact root: %w", err)
	}
	absoluteRoot = filepath.Clean(absoluteRoot)
	if absoluteRoot == string(filepath.Separator) {
		return nil, errors.New("filesystem root cannot be an artifact root")
	}
	if strings.Contains(filepath.ToSlash(absoluteRoot), "/.kurtosis/") || strings.HasSuffix(filepath.ToSlash(absoluteRoot), "/.kurtosis") {
		return nil, errors.New("cluster-lifecycle storage cannot be an artifact root")
	}
	if err := os.MkdirAll(absoluteRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create artifact root: %w", err)
	}
	rootInfo, err := os.Lstat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect artifact root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, errors.New("artifact root must be a real directory, not a symlink")
	}
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve artifact root symlinks: %w", err)
	}
	absoluteRoot = resolvedRoot
	runPath := filepath.Join(absoluteRoot, runID)
	if err := os.Mkdir(runPath, 0o700); err != nil {
		return nil, fmt.Errorf("create run artifact directory %q: %w", runPath, err)
	}
	return &Directory{path: runPath}, nil
}

func (directory *Directory) Path() string {
	return directory.path
}

func (directory *Directory) CreateFile(name string) (*os.File, error) {
	if !safeFileName(name) {
		return nil, fmt.Errorf("artifact file name %q is invalid", name)
	}
	path := filepath.Join(directory.path, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create artifact %q: %w", name, err)
	}
	return file, nil
}

func (directory *Directory) WriteJSON(name string, value any) error {
	file, err := directory.CreateFile(name)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	writeErr := encoder.Encode(value)
	closeErr := errors.Join(file.Sync(), file.Close())
	if writeErr != nil {
		return errors.Join(fmt.Errorf("encode artifact %q: %w", name, writeErr), closeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close artifact %q: %w", name, closeErr)
	}
	return nil
}

func (directory *Directory) Write(name string, reader io.Reader) error {
	if reader == nil {
		return errors.New("artifact reader is required")
	}
	file, err := directory.CreateFile(name)
	if err != nil {
		return err
	}
	_, writeErr := io.Copy(file, reader)
	closeErr := errors.Join(file.Sync(), file.Close())
	if writeErr != nil {
		return errors.Join(fmt.Errorf("write artifact %q: %w", name, writeErr), closeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close artifact %q: %w", name, closeErr)
	}
	return nil
}

func safeFileName(name string) bool {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return false
	}
	for _, character := range name {
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '.' {
			continue
		}
		return false
	}
	return true
}

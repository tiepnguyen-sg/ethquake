package experiment

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tiepnguyen-sg/ethquake/internal/scenario"
)

var checksummedRunArtifacts = []string{
	"manifest.json",
	"metadata.json",
	"raw-timeseries.jsonl",
	"realized-split.json",
	"scenario.yaml",
}

func LoadRunSummaries(root string, value scenario.Scenario) ([]RunSummary, error) {
	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" {
		return nil, errors.New("canonical evidence cannot be loaded from inside Kubernetes")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve runs root: %w", err)
	}
	rootInfo, err := os.Lstat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect runs root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, errors.New("runs root must be a real directory, not a symlink")
	}
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve runs root symlinks: %w", err)
	}
	cleanRoot := filepath.ToSlash(resolvedRoot)
	if strings.Contains(cleanRoot, "/.kurtosis/") || strings.HasSuffix(cleanRoot, "/.kurtosis") {
		return nil, errors.New("cluster-lifecycle storage cannot be an evidence root")
	}
	runs := make([]RunSummary, 0, len(value.Spec.Methodology.RunOrder))
	for _, runID := range value.Spec.Methodology.RunOrder {
		runDirectory := filepath.Join(resolvedRoot, runID)
		directoryInfo, err := os.Lstat(runDirectory)
		if err != nil {
			return nil, fmt.Errorf("inspect run directory %q: %w", runID, err)
		}
		if directoryInfo.Mode()&os.ModeSymlink != 0 || !directoryInfo.IsDir() {
			return nil, fmt.Errorf("run directory %q must be a real directory, not a symlink", runID)
		}
		if err := verifyRunChecksums(runDirectory); err != nil {
			return nil, fmt.Errorf("verify run %q: %w", runID, err)
		}
		path := filepath.Join(runDirectory, "manifest.json")
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect run manifest %q: %w", runID, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("run manifest %q must be a regular file, not a symlink", runID)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read run manifest %q: %w", runID, err)
		}
		run, err := parseRunSummary(data)
		if err != nil {
			return nil, fmt.Errorf("parse run manifest %q: %w", runID, err)
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func verifyRunChecksums(runDirectory string) error {
	path := filepath.Join(runDirectory, "checksums.json")
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect checksums: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("checksums.json must be a regular file, not a symlink")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read checksums: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var checksums map[string]string
	if err := decoder.Decode(&checksums); err != nil {
		return fmt.Errorf("decode checksums: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("checksums.json must contain exactly one JSON value")
		}
		return fmt.Errorf("decode trailing checksums: %w", err)
	}
	if len(checksums) != len(checksummedRunArtifacts) {
		return fmt.Errorf("checksums.json has %d entries; expected %d", len(checksums), len(checksummedRunArtifacts))
	}
	for _, name := range checksummedRunArtifacts {
		expected, exists := checksums[name]
		if !exists || !isLowerHex(expected, 64) {
			return fmt.Errorf("artifact %q has no valid SHA-256", name)
		}
		artifactPath := filepath.Join(runDirectory, name)
		artifactInfo, err := os.Lstat(artifactPath)
		if err != nil {
			return fmt.Errorf("inspect artifact %q: %w", name, err)
		}
		if artifactInfo.Mode()&os.ModeSymlink != 0 || !artifactInfo.Mode().IsRegular() {
			return fmt.Errorf("artifact %q must be a regular file, not a symlink", name)
		}
		file, err := os.Open(artifactPath)
		if err != nil {
			return fmt.Errorf("open artifact %q: %w", name, err)
		}
		hash := sha256.New()
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("hash artifact %q: %w", name, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close artifact %q: %w", name, closeErr)
		}
		actual := fmt.Sprintf("%x", hash.Sum(nil))
		if actual != expected {
			return fmt.Errorf("artifact %q checksum mismatch", name)
		}
	}
	return nil
}

func parseRunSummary(data []byte) (RunSummary, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value RunSummary
	if err := decoder.Decode(&value); err != nil {
		return RunSummary{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return RunSummary{}, errors.New("run manifest must contain exactly one JSON value")
		}
		return RunSummary{}, err
	}
	return value, nil
}

package experiment

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRunSummariesUsesCommittedOrder(t *testing.T) {
	value := readScenario(t)
	runs := validRuns(value, []uint64{5, 5, 5}, []uint64{8, 8, 8})
	root := t.TempDir()
	for _, run := range runs {
		directory := filepath.Join(root, run.RunID)
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		data, err := json.Marshal(run)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(filepath.Join(directory, "manifest.json"), data, 0o600); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
		writeChecksummedRunArtifacts(t, directory)
	}
	loaded, err := LoadRunSummaries(root, value)
	if err != nil {
		t.Fatalf("LoadRunSummaries() error = %v", err)
	}
	for index, runID := range value.Spec.Methodology.RunOrder {
		if loaded[index].RunID != runID {
			t.Fatalf("loaded order = %+v", loaded)
		}
	}
}

func TestLoadRunSummariesRejectsTamperedManifest(t *testing.T) {
	value := readScenario(t)
	runs := validRuns(value, []uint64{5, 5, 5}, []uint64{8, 8, 8})
	root := t.TempDir()
	for _, run := range runs {
		directory := filepath.Join(root, run.RunID)
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		data, err := json.Marshal(run)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(filepath.Join(directory, "manifest.json"), data, 0o600); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
		writeChecksummedRunArtifacts(t, directory)
	}
	path := filepath.Join(root, runs[0].RunID, "manifest.json")
	if err := os.WriteFile(path, []byte(`{"tampered":true}`), 0o600); err != nil {
		t.Fatalf("tamper manifest: %v", err)
	}
	if _, err := LoadRunSummaries(root, value); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("LoadRunSummaries() error = %v", err)
	}
}

func TestParseRunSummaryRejectsUnknownField(t *testing.T) {
	_, err := parseRunSummary([]byte(`{"schema_version":"ethquake.run/v1alpha1","unknown":true}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("parseRunSummary() error = %v", err)
	}
}

func writeChecksummedRunArtifacts(t *testing.T, directory string) {
	t.Helper()
	checksums := make(map[string]string, len(checksummedRunArtifacts))
	for _, name := range checksummedRunArtifacts {
		path := filepath.Join(directory, name)
		if name != "manifest.json" {
			if err := os.WriteFile(path, []byte(name+"\n"), 0o600); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		digest := sha256.Sum256(data)
		checksums[name] = fmt.Sprintf("%x", digest[:])
	}
	data, err := json.Marshal(checksums)
	if err != nil {
		t.Fatalf("marshal checksums: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "checksums.json"), data, 0o600); err != nil {
		t.Fatalf("write checksums: %v", err)
	}
}

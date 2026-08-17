package dependency

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryLockParses(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "experiment", "dependencies.lock.json"))
	if err != nil {
		t.Fatalf("read dependency lock: %v", err)
	}
	lock, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if lock.EthereumPackage.Revision == "" || len(CanonicalImageList(lock)) != len(lock.RuntimeImages)+2 {
		t.Fatalf("lock = %+v", lock)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	data := `{"schema_version":"ethquake.dependencies/v1alpha1","unknown":true}`
	_, err := Parse([]byte(data))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestParseRejectsOversizedInput(t *testing.T) {
	_, err := Parse(bytes.Repeat([]byte{'x'}, maxLockBytes+1))
	if err == nil || !strings.Contains(err.Error(), "input limit") {
		t.Fatalf("Parse() error = %v", err)
	}
}

func TestExperimentMetadataRejectsUnlockedImage(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "experiment", "dependencies.lock.json"))
	if err != nil {
		t.Fatalf("read dependency lock: %v", err)
	}
	lock, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	_, err = lock.ExperimentMetadata(map[string]string{"cl-1": "sigp/lighthouse:latest"})
	if err == nil || !strings.Contains(err.Error(), "outside the dependency lock") {
		t.Fatalf("ExperimentMetadata() error = %v", err)
	}
}

func TestExperimentMetadataRecordsLockAndRealizedWorkloadImages(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "experiment", "dependencies.lock.json"))
	if err != nil {
		t.Fatalf("read dependency lock: %v", err)
	}
	lock, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	image := lock.RuntimeImages["sigp/lighthouse"]
	metadata, err := lock.ExperimentMetadata(map[string]string{"experiment/cl-1/main": image})
	if err != nil {
		t.Fatalf("ExperimentMetadata() error = %v", err)
	}
	if metadata.RuntimeImages["workload/experiment/cl-1/main"] != image ||
		metadata.RuntimeImages["lock/runtime/sigp/lighthouse"] != image ||
		metadata.RuntimeImages["lock/fault/controller"] != lock.FaultRuntime.ControllerImage {
		t.Fatalf("runtime image metadata = %+v", metadata.RuntimeImages)
	}
}

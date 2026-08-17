// Package dependency validates the immutable experiment dependency closure.
package dependency

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/tiepnguyen-sg/ethquake/internal/experiment"
)

const SchemaVersion = "ethquake.dependencies/v1alpha1"

const maxLockBytes = 1 << 20

var (
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern = regexp.MustCompile(`^[^@[:space:]]+(?::[^@[:space:]]+)?@sha256:[0-9a-f]{64}$`)
)

type Lock struct {
	SchemaVersion    string            `json:"schema_version"`
	EthereumPackage  Source            `json:"ethereum_package"`
	ImportedPackages map[string]string `json:"imported_packages"`
	RuntimeImages    map[string]string `json:"runtime_images"`
	FaultRuntime     FaultRuntime      `json:"fault_runtime"`
}

type Source struct {
	Repository    string `json:"repository"`
	Revision      string `json:"revision"`
	OverlaySHA256 string `json:"overlay_sha256"`
}

type FaultRuntime struct {
	ChaosMeshRevision string `json:"chaos_mesh_revision"`
	ControllerImage   string `json:"controller_image"`
	DaemonImage       string `json:"daemon_image"`
}

func Parse(data []byte) (Lock, error) {
	if len(data) > maxLockBytes {
		return Lock{}, fmt.Errorf("dependency lock exceeds the %d-byte input limit", maxLockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value Lock
	if err := decoder.Decode(&value); err != nil {
		return Lock{}, fmt.Errorf("decode dependency lock: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Lock{}, errors.New("dependency lock must contain exactly one JSON value")
		}
		return Lock{}, fmt.Errorf("decode trailing dependency lock: %w", err)
	}
	if err := value.Validate(); err != nil {
		return Lock{}, err
	}
	return value, nil
}

func (lock Lock) Validate() error {
	if lock.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported dependency schema_version %q", lock.SchemaVersion)
	}
	if lock.EthereumPackage.Repository != "https://github.com/ethpandaops/ethereum-package.git" {
		return errors.New("ethereum-package repository is not the accepted upstream")
	}
	if !commitPattern.MatchString(lock.EthereumPackage.Revision) {
		return errors.New("ethereum-package revision must be a full lowercase commit SHA")
	}
	if !hexDigest(lock.EthereumPackage.OverlaySHA256) {
		return errors.New("ethereum-package overlay_sha256 must be a lowercase SHA-256")
	}
	if len(lock.ImportedPackages) == 0 || len(lock.RuntimeImages) == 0 {
		return errors.New("imported_packages and runtime_images are required")
	}
	for name, revision := range lock.ImportedPackages {
		if name == "" || !commitPattern.MatchString(revision) {
			return fmt.Errorf("imported package %q is not pinned to a full commit SHA", name)
		}
	}
	for name, image := range lock.RuntimeImages {
		if name == "" || !digestPattern.MatchString(image) {
			return fmt.Errorf("runtime image %q is not pinned by OCI digest", name)
		}
	}
	if !commitPattern.MatchString(lock.FaultRuntime.ChaosMeshRevision) {
		return errors.New("Chaos Mesh revision must be a full commit SHA")
	}
	if !digestPattern.MatchString(lock.FaultRuntime.ControllerImage) || !digestPattern.MatchString(lock.FaultRuntime.DaemonImage) {
		return errors.New("Chaos Mesh runtime images must be pinned by OCI digest")
	}
	return nil
}

func (lock Lock) ExperimentMetadata(resolvedImages map[string]string) (experiment.DependencyMetadata, error) {
	if err := lock.Validate(); err != nil {
		return experiment.DependencyMetadata{}, err
	}
	if len(resolvedImages) == 0 {
		return experiment.DependencyMetadata{}, errors.New("resolved runtime image metadata is required")
	}
	allowed := make(map[string]struct{}, len(lock.RuntimeImages)+2)
	for _, image := range lock.RuntimeImages {
		allowed[image] = struct{}{}
	}
	allowed[lock.FaultRuntime.ControllerImage] = struct{}{}
	allowed[lock.FaultRuntime.DaemonImage] = struct{}{}
	for workload, image := range resolvedImages {
		if workload == "" {
			return experiment.DependencyMetadata{}, errors.New("resolved runtime workload name is empty")
		}
		if _, exists := allowed[image]; !exists {
			return experiment.DependencyMetadata{}, fmt.Errorf("workload %q uses image outside the dependency lock: %s", workload, image)
		}
	}
	metadataImages := make(map[string]string, len(resolvedImages)+len(lock.RuntimeImages)+2)
	for workload, image := range resolvedImages {
		metadataImages["workload/"+workload] = image
	}
	for name, image := range lock.RuntimeImages {
		metadataImages["lock/runtime/"+name] = image
	}
	metadataImages["lock/fault/controller"] = lock.FaultRuntime.ControllerImage
	metadataImages["lock/fault/daemon"] = lock.FaultRuntime.DaemonImage
	return experiment.DependencyMetadata{
		EthereumPackageRevision: lock.EthereumPackage.Revision,
		ImportedPackages:        cloneMap(lock.ImportedPackages),
		RuntimeImages:           metadataImages,
	}, nil
}

func CanonicalImageList(lock Lock) []string {
	values := make([]string, 0, len(lock.RuntimeImages)+2)
	for _, image := range lock.RuntimeImages {
		values = append(values, image)
	}
	values = append(values, lock.FaultRuntime.ControllerImage, lock.FaultRuntime.DaemonImage)
	sort.Strings(values)
	return values
}

func hexDigest(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}

func cloneMap(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

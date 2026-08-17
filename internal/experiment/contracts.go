package experiment

import (
	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
	"github.com/tiepnguyen-sg/ethquake/internal/topology"
)

const MetadataSchemaVersion = "ethquake.metadata/v1alpha1"

// RunMetadata records the runtime inputs needed to adjudicate one run after
// its ephemeral network no longer exists.
type RunMetadata struct {
	SchemaVersion       string                    `json:"schema_version"`
	RunID               string                    `json:"run_id"`
	ScenarioSHA256      string                    `json:"scenario_sha256"`
	ChainID             uint64                    `json:"chain_id"`
	RuntimeSpecs        map[string]beacon.Spec    `json:"runtime_specs"`
	RuntimeGenesis      map[string]beacon.Genesis `json:"runtime_genesis"`
	Placements          []topology.Placement      `json:"placements"`
	Dependencies        DependencyMetadata        `json:"dependencies"`
	OrderSeed           string                    `json:"order_seed"`
	RunOrder            []string                  `json:"run_order"`
	FaultDeadmanSeconds uint64                    `json:"fault_deadman_seconds"`
}

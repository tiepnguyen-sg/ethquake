package topology

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiepnguyen-sg/ethquake/internal/scenario"
)

func TestNewKubernetesAllowsOnlyGKEPhase3Context(t *testing.T) {
	if _, err := NewKubernetes("kubectl", "kubeconfig", "gke-ethquake-phase3"); err != nil {
		t.Fatalf("NewKubernetes() error = %v", err)
	}
	if _, err := NewKubernetes("kubectl", "kubeconfig", "untrusted-context"); err == nil {
		t.Fatal("NewKubernetes() accepted an untrusted context")
	}
}

func TestBuildDiscoveryRequiresPinnedImagesAndControlledPlacement(t *testing.T) {
	value := readScenarioForKubernetes(t)
	pods, nodes := discoveryFixtures(value)
	chaos := []podResource{fixturePod("chaos-controller", "", "system", pinnedImage("chaos"))}
	discovery, err := buildDiscovery(value, pods, chaos, nodes)
	if err != nil {
		t.Fatalf("buildDiscovery() error = %v", err)
	}
	if len(discovery.Placements) != 4 || len(discovery.RuntimeImages) != 13 {
		t.Fatalf("discovery = %+v", discovery)
	}
	pods[0].Spec.Containers[0].Image = "client:latest"
	if _, err := buildDiscovery(value, pods, chaos, nodes); err == nil || !strings.Contains(err.Error(), "not digest-pinned") {
		t.Fatalf("buildDiscovery() error = %v", err)
	}
}

func TestBuildDiscoveryRejectsMutableInitContainerImage(t *testing.T) {
	value := readScenarioForKubernetes(t)
	pods, nodes := discoveryFixtures(value)
	chaos := fixturePod("chaos-controller", "", "system", pinnedImage("chaos"))
	chaos.Spec.InitContainers = []containerSpec{{Name: "setup", Image: "example.invalid/setup:latest"}}
	if _, err := buildDiscovery(value, pods, []podResource{chaos}, nodes); err == nil || !strings.Contains(err.Error(), "not digest-pinned") {
		t.Fatalf("buildDiscovery() error = %v", err)
	}
}

func TestBuildDiscoveryRejectsSplitParticipantServices(t *testing.T) {
	value := readScenarioForKubernetes(t)
	pods, nodes := discoveryFixtures(value)
	pods[1].Spec.NodeName = "node-b"
	if _, err := buildDiscovery(value, pods, nil, nodes); err == nil || !strings.Contains(err.Error(), "not co-located") {
		t.Fatalf("buildDiscovery() error = %v", err)
	}
}

func TestBuildDiscoveryRejectsUnexpectedExperimentWorkload(t *testing.T) {
	value := readScenarioForKubernetes(t)
	pods, nodes := discoveryFixtures(value)
	pods = append(pods, fixturePod("unexpected", "unexpected", "node-a", pinnedImage("unexpected")))
	if _, err := buildDiscovery(value, pods, nil, nodes); err == nil || !strings.Contains(err.Error(), "unexpected service ID") {
		t.Fatalf("buildDiscovery() error = %v", err)
	}
}

func TestBuildDiscoveryRequiresReadyChaosMeshWorkloads(t *testing.T) {
	value := readScenarioForKubernetes(t)
	pods, nodes := discoveryFixtures(value)
	chaos := fixturePod("chaos-controller", "", "system", pinnedImage("chaos"))
	chaos.Status.ContainerStatuses[0].Ready = false
	if _, err := buildDiscovery(value, pods, []podResource{chaos}, nodes); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("buildDiscovery() error = %v", err)
	}
}

func discoveryFixtures(value scenario.Scenario) ([]podResource, map[string]nodeResource) {
	pods := make([]podResource, 0, 12)
	nodes := make(map[string]nodeResource, 4)
	for index, participant := range value.Spec.Topology.Participants {
		nodeName := "node-" + string(rune('a'+index))
		for _, serviceID := range []string{participant.ExecutionServiceID, participant.BeaconServiceID, participant.ValidatorServiceID} {
			pods = append(pods, fixturePod(serviceID, serviceID, nodeName, pinnedImage(serviceID)))
		}
		var node nodeResource
		node.Metadata.Name = nodeName
		node.Metadata.Labels = map[string]string{
			placementLabel:   "ethquake-p" + string(rune('1'+index)),
			machineTypeLabel: "candidate-instance",
		}
		nodes[nodeName] = node
	}
	return pods, nodes
}

func fixturePod(name, serviceID, node, image string) podResource {
	var pod podResource
	pod.Metadata.Name = name
	pod.Metadata.Labels = map[string]string{serviceIDLabel: serviceID}
	pod.Spec.NodeName = node
	pod.Spec.Containers = []containerSpec{{
		Name:  "main",
		Image: image,
		Resources: resourcePolicy{
			Requests: map[string]string{"cpu": "1", "memory": "2Gi"},
			Limits:   map[string]string{"cpu": "2", "memory": "4Gi"},
		},
	}}
	pod.Status.Phase = "Running"
	pod.Status.ContainerStatuses = []struct {
		Name  string `json:"name"`
		Ready bool   `json:"ready"`
	}{{Name: "main", Ready: true}}
	return pod
}

func pinnedImage(name string) string {
	return "example.invalid/" + name + "@sha256:" + strings.Repeat("a", 64)
}

func readScenarioForKubernetes(t *testing.T) scenario.Scenario {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "scenarios", "cl-p2p-partition.yaml"))
	if err != nil {
		t.Fatalf("read scenario: %v", err)
	}
	value, err := scenario.Parse(data)
	if err != nil {
		t.Fatalf("parse scenario: %v", err)
	}
	return value
}

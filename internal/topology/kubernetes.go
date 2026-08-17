package topology

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tiepnguyen-sg/ethquake/internal/scenario"
)

const (
	managedLabel      = "dev.ethquake.managed"
	phaseLabel        = "dev.ethquake.phase"
	runIDLabel        = "dev.ethquake.run-id"
	serviceIDLabel    = "kurtosistech.com/id"
	resourceTypeLabel = "kurtosistech.com/resource-type"
	placementLabel    = "dev.ethquake.participant"
	machineTypeLabel  = "node.kubernetes.io/instance-type"
)

type Discovery struct {
	Placements    []Placement       `json:"placements"`
	RuntimeImages map[string]string `json:"runtime_images"`
}

type kubectlRunner interface {
	Run(context.Context, ...string) ([]byte, error)
}

type Kubernetes struct {
	runner kubectlRunner
}

func NewKubernetes(kubectlPath, kubeconfigPath, contextName string) (*Kubernetes, error) {
	if kubectlPath == "" || kubeconfigPath == "" {
		return nil, errors.New("kubectl and kubeconfig paths are required")
	}
	absoluteKubeconfig, err := filepath.Abs(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("resolve kubeconfig path: %w", err)
	}
	if contextName != "ethquake-aws-phase3" {
		return nil, fmt.Errorf("Kubernetes context %q is not the Phase 3 evidence context", contextName)
	}
	return &Kubernetes{runner: &kubectlExec{
		path: kubectlPath,
		base: []string{"--kubeconfig", absoluteKubeconfig, "--context", contextName},
	}}, nil
}

func (kubernetes *Kubernetes) Discover(ctx context.Context, value scenario.Scenario, runID, namespace string) (Discovery, error) {
	if namespace != value.Spec.Target.NamespacePrefix+runID {
		return Discovery{}, fmt.Errorf("namespace %q does not match the exact Phase 3 run target", namespace)
	}
	if err := kubernetes.verifyNamespace(ctx, runID, namespace); err != nil {
		return Discovery{}, err
	}
	userPods, err := kubernetes.listPods(ctx, namespace, resourceTypeLabel+"=user-service")
	if err != nil {
		return Discovery{}, fmt.Errorf("discover experiment workloads: %w", err)
	}
	chaosPods, err := kubernetes.listPods(ctx, "chaos-mesh", "")
	if err != nil {
		return Discovery{}, fmt.Errorf("discover Chaos Mesh workloads: %w", err)
	}
	nodeNames := make(map[string]struct{})
	for _, pod := range userPods {
		if pod.Spec.NodeName != "" {
			nodeNames[pod.Spec.NodeName] = struct{}{}
		}
	}
	nodes := make(map[string]nodeResource, len(nodeNames))
	for nodeName := range nodeNames {
		node, err := kubernetes.getNode(ctx, nodeName)
		if err != nil {
			return Discovery{}, err
		}
		nodes[nodeName] = node
	}
	discovery, err := buildDiscovery(value, userPods, chaosPods, nodes)
	if err != nil {
		return Discovery{}, err
	}
	return discovery, nil
}

func (kubernetes *Kubernetes) verifyNamespace(ctx context.Context, runID, namespace string) error {
	output, err := kubernetes.runner.Run(ctx, "get", "namespace", namespace, "--output=json")
	if err != nil {
		return fmt.Errorf("read experiment namespace: %w", err)
	}
	var resource struct {
		Metadata metadata `json:"metadata"`
	}
	if err := json.Unmarshal(output, &resource); err != nil {
		return fmt.Errorf("decode experiment namespace: %w", err)
	}
	if resource.Metadata.Labels[managedLabel] != "true" || resource.Metadata.Labels[phaseLabel] != "3" || resource.Metadata.Labels[runIDLabel] != runID {
		return errors.New("experiment namespace lacks exact Ethquake Phase 3 ownership labels")
	}
	return nil
}

func (kubernetes *Kubernetes) listPods(ctx context.Context, namespace, selector string) ([]podResource, error) {
	arguments := []string{"--namespace", namespace, "get", "pods"}
	if selector != "" {
		arguments = append(arguments, "--selector", selector)
	}
	arguments = append(arguments, "--output=json")
	output, err := kubernetes.runner.Run(ctx, arguments...)
	if err != nil {
		return nil, err
	}
	var list struct {
		Items []podResource `json:"items"`
	}
	if err := json.Unmarshal(output, &list); err != nil {
		return nil, fmt.Errorf("decode Pod list: %w", err)
	}
	if len(list.Items) == 0 {
		return nil, fmt.Errorf("namespace %q has no matching Pods", namespace)
	}
	return list.Items, nil
}

func (kubernetes *Kubernetes) getNode(ctx context.Context, name string) (nodeResource, error) {
	output, err := kubernetes.runner.Run(ctx, "get", "node", name, "--output=json")
	if err != nil {
		return nodeResource{}, fmt.Errorf("read node %q: %w", name, err)
	}
	var node nodeResource
	if err := json.Unmarshal(output, &node); err != nil {
		return nodeResource{}, fmt.Errorf("decode node %q: %w", name, err)
	}
	return node, nil
}

type metadata struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

type resourcePolicy struct {
	Requests map[string]string `json:"requests"`
	Limits   map[string]string `json:"limits"`
}

type containerSpec struct {
	Name      string         `json:"name"`
	Image     string         `json:"image"`
	Resources resourcePolicy `json:"resources"`
}

type podResource struct {
	Metadata metadata `json:"metadata"`
	Spec     struct {
		NodeName       string          `json:"nodeName"`
		InitContainers []containerSpec `json:"initContainers"`
		Containers     []containerSpec `json:"containers"`
	} `json:"spec"`
	Status struct {
		Phase             string `json:"phase"`
		ContainerStatuses []struct {
			Name  string `json:"name"`
			Ready bool   `json:"ready"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

type nodeResource struct {
	Metadata metadata `json:"metadata"`
}

func buildDiscovery(value scenario.Scenario, userPods, chaosPods []podResource, nodes map[string]nodeResource) (Discovery, error) {
	podsByService := make(map[string][]podResource)
	runtimeImages := make(map[string]string)
	for _, namespacedPods := range []struct {
		namespace string
		pods      []podResource
	}{{namespace: "experiment", pods: userPods}, {namespace: "chaos-mesh", pods: chaosPods}} {
		for _, pod := range namespacedPods.pods {
			if pod.Metadata.Name == "" || len(pod.Spec.Containers) == 0 {
				return Discovery{}, errors.New("discovered Pod has no name or containers")
			}
			workloadID := "runtime"
			if namespacedPods.namespace == "experiment" {
				serviceID := pod.Metadata.Labels[serviceIDLabel]
				if serviceID == "" {
					return Discovery{}, fmt.Errorf("experiment Pod %q has no stable Kurtosis service ID", pod.Metadata.Name)
				}
				podsByService[serviceID] = append(podsByService[serviceID], pod)
				workloadID = serviceID
			}
			containers := append(append([]containerSpec(nil), pod.Spec.InitContainers...), pod.Spec.Containers...)
			for _, container := range containers {
				if !digestPinned(container.Image) {
					return Discovery{}, fmt.Errorf("Pod %q container %q is not digest-pinned: %s", pod.Metadata.Name, container.Name, container.Image)
				}
				key := namespacedPods.namespace + "/" + workloadID + "/" + container.Name
				if existing, duplicate := runtimeImages[key]; duplicate && existing != container.Image {
					return Discovery{}, fmt.Errorf("stable workload %q resolved to conflicting images", key)
				}
				runtimeImages[key] = container.Image
			}
		}
	}
	placements := make([]Placement, 0, len(value.Spec.Topology.Participants))
	for _, participant := range value.Spec.Topology.Participants {
		services := []struct {
			role string
			id   string
		}{
			{role: "execution", id: participant.ExecutionServiceID},
			{role: "beacon", id: participant.BeaconServiceID},
			{role: "validator", id: participant.ValidatorServiceID},
		}
		rolePolicies := make(map[string]resourcePolicy, len(services))
		participantNode := ""
		beaconPod := ""
		for _, service := range services {
			matches := podsByService[service.id]
			if len(matches) != 1 {
				return Discovery{}, fmt.Errorf("service %q resolved to %d Pods; expected one", service.id, len(matches))
			}
			pod := matches[0]
			if err := requireReady(pod); err != nil {
				return Discovery{}, err
			}
			if participantNode == "" {
				participantNode = pod.Spec.NodeName
			} else if pod.Spec.NodeName != participantNode {
				return Discovery{}, fmt.Errorf("participant %q services are not co-located on one controlled node", participant.ID)
			}
			if len(pod.Spec.Containers) != 1 {
				return Discovery{}, fmt.Errorf("service %q has %d containers; expected one", service.id, len(pod.Spec.Containers))
			}
			rolePolicies[service.role] = pod.Spec.Containers[0].Resources
			if service.role == "beacon" {
				beaconPod = pod.Metadata.Name
			}
		}
		node, exists := nodes[participantNode]
		if !exists {
			return Discovery{}, fmt.Errorf("node metadata for %q is missing", participantNode)
		}
		policyJSON, err := json.Marshal(rolePolicies)
		if err != nil {
			return Discovery{}, fmt.Errorf("encode participant %q resource policy: %w", participant.ID, err)
		}
		placements = append(placements, Placement{
			ParticipantID:  participant.ID,
			Target:         participant.BeaconTarget,
			Pod:            beaconPod,
			Node:           participantNode,
			NodePool:       node.Metadata.Labels[placementLabel],
			MachineType:    node.Metadata.Labels[machineTypeLabel],
			ResourcePolicy: string(policyJSON),
		})
	}
	return Discovery{Placements: placements, RuntimeImages: runtimeImages}, nil
}

func requireReady(pod podResource) error {
	if pod.Status.Phase != "Running" || len(pod.Status.ContainerStatuses) != len(pod.Spec.Containers) {
		return fmt.Errorf("Pod %q is not Running with complete container status", pod.Metadata.Name)
	}
	for _, status := range pod.Status.ContainerStatuses {
		if !status.Ready {
			return fmt.Errorf("Pod %q container %q is not ready", pod.Metadata.Name, status.Name)
		}
	}
	if pod.Spec.NodeName == "" {
		return fmt.Errorf("Pod %q is not scheduled", pod.Metadata.Name)
	}
	return nil
}

func digestPinned(image string) bool {
	parts := strings.Split(image, "@sha256:")
	if len(parts) != 2 || parts[0] == "" || len(parts[1]) != 64 {
		return false
	}
	_, err := hex.DecodeString(parts[1])
	return err == nil && strings.ToLower(parts[1]) == parts[1]
}

func ResourcePolicyDigest(policy string) string {
	digest := sha256.Sum256([]byte(policy))
	return hex.EncodeToString(digest[:])
}

type kubectlExec struct {
	path string
	base []string
}

func (runner *kubectlExec) Run(ctx context.Context, arguments ...string) ([]byte, error) {
	commandArguments := append(append([]string(nil), runner.base...), arguments...)
	command := exec.CommandContext(ctx, runner.path, commandArguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 4096 {
			message = message[:4096] + "..."
		}
		return nil, fmt.Errorf("kubectl command failed: %s: %w", message, err)
	}
	return output, nil
}

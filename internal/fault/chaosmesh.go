package fault

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	managedLabel = "dev.ethquake.managed"
	runIDLabel   = "dev.ethquake.run-id"
	phaseLabel   = "dev.ethquake.phase"
)

var dnsNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type commandRunner interface {
	Run(context.Context, []byte, ...string) ([]byte, error)
}

type ChaosMesh struct {
	runner commandRunner
}

func NewChaosMesh(kubectlPath, kubeconfigPath, contextName string) (*ChaosMesh, error) {
	if kubectlPath == "" {
		return nil, errors.New("kubectl path is required")
	}
	if kubeconfigPath == "" {
		return nil, errors.New("kubeconfig path is required")
	}
	absoluteKubeconfig, err := filepath.Abs(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("resolve kubeconfig path: %w", err)
	}
	if contextName != "kind-ethquake" &&
		contextName != "kind-ethquake-chaos-smoke" &&
		contextName != "gke-ethquake-phase3" {
		return nil, fmt.Errorf("Kubernetes context %q is not allowlisted", contextName)
	}
	return &ChaosMesh{runner: &execRunner{
		path: kubectlPath,
		baseArguments: []string{
			"--kubeconfig", absoluteKubeconfig,
			"--context", contextName,
		},
	}}, nil
}

func (backend *ChaosMesh) Apply(ctx context.Context, request PartitionRequest) error {
	if err := validatePartitionRequest(request); err != nil {
		return err
	}
	if err := backend.verifyNamespaceOwnership(ctx, request); err != nil {
		return err
	}
	manifest, err := renderNetworkChaos(request)
	if err != nil {
		return err
	}
	name := resourceName(request.RunID)
	if _, err := backend.runner.Run(ctx, manifest,
		"--namespace", request.Namespace,
		"create", "--filename", "-",
	); err != nil {
		return fmt.Errorf("create NetworkChaos %q: %w", name, err)
	}
	if _, err := backend.runner.Run(ctx, nil,
		"--namespace", request.Namespace,
		"wait", "--for=condition=AllInjected",
		"networkchaos/"+name, "--timeout=60s",
	); err != nil {
		cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancelCleanup()
		revertErr := backend.Revert(cleanupContext, request)
		return errors.Join(fmt.Errorf("wait for NetworkChaos %q injection: %w", name, err), revertErr)
	}
	return nil
}

func (backend *ChaosMesh) Revert(ctx context.Context, request PartitionRequest) error {
	if err := validatePartitionRequest(request); err != nil {
		return err
	}
	if err := backend.verifyNamespaceOwnership(ctx, request); err != nil {
		return err
	}
	object, err := backend.getOwnedResource(ctx, request)
	if err != nil {
		return err
	}
	if object == nil {
		return nil
	}
	name := resourceName(request.RunID)
	if _, err := backend.runner.Run(ctx, nil,
		"--namespace", request.Namespace,
		"delete", "networkchaos", name,
		"--wait=true", "--timeout=60s",
	); err != nil {
		return fmt.Errorf("delete NetworkChaos %q: %w", name, err)
	}
	return nil
}

func (backend *ChaosMesh) Status(ctx context.Context, request PartitionRequest) (Status, error) {
	if err := validatePartitionRequest(request); err != nil {
		return Status{}, err
	}
	if err := backend.verifyNamespaceOwnership(ctx, request); err != nil {
		return Status{}, err
	}
	object, err := backend.getOwnedResource(ctx, request)
	if err != nil {
		return Status{}, err
	}
	if object == nil {
		return Status{}, nil
	}
	result := Status{Exists: true}
	for _, condition := range object.Status.Conditions {
		if condition.Status != "True" {
			continue
		}
		switch condition.Type {
		case "AllInjected":
			result.AllInjected = true
		case "AllRecovered":
			result.AllRecovered = true
		}
	}
	return result, nil
}

func (backend *ChaosMesh) verifyNamespaceOwnership(ctx context.Context, request PartitionRequest) error {
	output, err := backend.runner.Run(ctx, nil, "get", "namespace", request.Namespace, "--output=json")
	if err != nil {
		return fmt.Errorf("read target namespace %q: %w", request.Namespace, err)
	}
	var namespace struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(output, &namespace); err != nil {
		return fmt.Errorf("decode target namespace %q: %w", request.Namespace, err)
	}
	if namespace.Metadata.Labels[managedLabel] != "true" ||
		namespace.Metadata.Labels[phaseLabel] != "3" ||
		namespace.Metadata.Labels[runIDLabel] != request.RunID {
		return fmt.Errorf("%w: namespace %q lacks exact Ethquake Phase 3 labels", ErrOwnership, request.Namespace)
	}
	return nil
}

type networkChaosResource struct {
	Metadata struct {
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Status struct {
		Conditions []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
		} `json:"conditions"`
	} `json:"status"`
}

func (backend *ChaosMesh) getOwnedResource(ctx context.Context, request PartitionRequest) (*networkChaosResource, error) {
	name := resourceName(request.RunID)
	output, err := backend.runner.Run(ctx, nil,
		"--namespace", request.Namespace,
		"get", "networkchaos", name,
		"--ignore-not-found", "--output=json",
	)
	if err != nil {
		return nil, fmt.Errorf("read NetworkChaos %q: %w", name, err)
	}
	if len(bytes.TrimSpace(output)) == 0 {
		return nil, nil
	}
	var object networkChaosResource
	if err := json.Unmarshal(output, &object); err != nil {
		return nil, fmt.Errorf("decode NetworkChaos %q: %w", name, err)
	}
	if object.Metadata.Labels[managedLabel] != "true" ||
		object.Metadata.Labels[phaseLabel] != "3" ||
		object.Metadata.Labels[runIDLabel] != request.RunID {
		return nil, fmt.Errorf("%w: NetworkChaos %q labels do not match", ErrOwnership, name)
	}
	return &object, nil
}

func validatePartitionRequest(request PartitionRequest) error {
	if !dnsNamePattern.MatchString(request.RunID) {
		return fmt.Errorf("run ID %q is invalid", request.RunID)
	}
	if len(resourceName(request.RunID)) > 63 {
		return fmt.Errorf("run ID %q produces an overlong NetworkChaos name", request.RunID)
	}
	if request.NamespacePrefix != "kt-ethquake-phase3-" ||
		!strings.HasPrefix(request.Namespace, request.NamespacePrefix) ||
		!dnsNamePattern.MatchString(request.Namespace) {
		return fmt.Errorf("namespace %q is outside the Phase 3 allowlist", request.Namespace)
	}
	if request.TTL <= 0 || request.TTL%time.Second != 0 {
		return errors.New("fault TTL must be a positive whole-second duration")
	}
	if len(request.GroupA) == 0 || len(request.GroupB) == 0 {
		return errors.New("both partition groups require at least one Pod")
	}
	seen := make(map[string]struct{}, len(request.GroupA)+len(request.GroupB))
	for _, group := range [][]string{request.GroupA, request.GroupB} {
		for _, pod := range group {
			if !dnsNamePattern.MatchString(pod) {
				return fmt.Errorf("Pod name %q is invalid", pod)
			}
			if _, exists := seen[pod]; exists {
				return fmt.Errorf("Pod %q appears in both partition groups", pod)
			}
			seen[pod] = struct{}{}
		}
	}
	return nil
}

func renderNetworkChaos(request PartitionRequest) ([]byte, error) {
	groupA := append([]string(nil), request.GroupA...)
	groupB := append([]string(nil), request.GroupB...)
	sort.Strings(groupA)
	sort.Strings(groupB)
	type selector struct {
		Namespaces []string            `json:"namespaces"`
		Pods       map[string][]string `json:"pods"`
	}
	type podSelector struct {
		Mode     string   `json:"mode"`
		Selector selector `json:"selector"`
	}
	object := struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Metadata   struct {
			Name      string            `json:"name"`
			Namespace string            `json:"namespace"`
			Labels    map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec struct {
			Action    string      `json:"action"`
			Mode      string      `json:"mode"`
			Selector  selector    `json:"selector"`
			Direction string      `json:"direction"`
			Target    podSelector `json:"target"`
			Duration  string      `json:"duration"`
		} `json:"spec"`
	}{
		APIVersion: "chaos-mesh.org/v1alpha1",
		Kind:       "NetworkChaos",
	}
	object.Metadata.Name = resourceName(request.RunID)
	object.Metadata.Namespace = request.Namespace
	object.Metadata.Labels = map[string]string{
		managedLabel: "true",
		phaseLabel:   "3",
		runIDLabel:   request.RunID,
	}
	object.Spec.Action = "partition"
	object.Spec.Mode = "all"
	object.Spec.Selector = selector{
		Namespaces: []string{request.Namespace},
		Pods:       map[string][]string{request.Namespace: groupA},
	}
	object.Spec.Direction = "both"
	object.Spec.Target = podSelector{
		Mode: "all",
		Selector: selector{
			Namespaces: []string{request.Namespace},
			Pods:       map[string][]string{request.Namespace: groupB},
		},
	}
	object.Spec.Duration = fmt.Sprintf("%ds", int64(request.TTL/time.Second))
	manifest, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("encode NetworkChaos manifest: %w", err)
	}
	return append(manifest, '\n'), nil
}

func resourceName(runID string) string {
	return "ethquake-" + runID + "-cl-partition"
}

type execRunner struct {
	path          string
	baseArguments []string
}

func (runner *execRunner) Run(ctx context.Context, stdin []byte, arguments ...string) ([]byte, error) {
	commandArguments := append(append([]string(nil), runner.baseArguments...), arguments...)
	command := exec.CommandContext(ctx, runner.path, commandArguments...)
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if len(message) > 4096 {
			message = message[:4096] + "..."
		}
		if message == "" {
			return nil, fmt.Errorf("kubectl command failed: %w", err)
		}
		return nil, fmt.Errorf("kubectl command failed: %s: %w", message, err)
	}
	return output, nil
}

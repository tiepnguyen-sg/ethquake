package fault

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNewChaosMeshContextAllowlist(t *testing.T) {
	for _, contextName := range []string{
		"kind-ethquake",
		"kind-ethquake-chaos-smoke",
		"ethquake-aws-phase3",
	} {
		if _, err := NewChaosMesh("kubectl", "kubeconfig", contextName); err != nil {
			t.Fatalf("NewChaosMesh(%q) error = %v", contextName, err)
		}
	}
	if _, err := NewChaosMesh("kubectl", "kubeconfig", "untrusted-context"); err == nil {
		t.Fatal("NewChaosMesh() accepted an untrusted context")
	}
}

type runnerCall struct {
	stdin     []byte
	arguments []string
}

type fakeRunner struct {
	responses [][]byte
	errors    []error
	calls     []runnerCall
}

func (runner *fakeRunner) Run(_ context.Context, stdin []byte, arguments ...string) ([]byte, error) {
	runner.calls = append(runner.calls, runnerCall{
		stdin:     append([]byte(nil), stdin...),
		arguments: append([]string(nil), arguments...),
	})
	index := len(runner.calls) - 1
	var response []byte
	var err error
	if index < len(runner.responses) {
		response = runner.responses[index]
	}
	if index < len(runner.errors) {
		err = runner.errors[index]
	}
	return response, err
}

func TestChaosMeshApplyCreatesBoundedPartition(t *testing.T) {
	runner := &fakeRunner{responses: [][]byte{ownedNamespaceJSON(), nil, nil}}
	backend := &ChaosMesh{runner: runner}
	request := validRequest()
	if err := backend.Apply(context.Background(), request); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("calls = %d", len(runner.calls))
	}
	var manifest map[string]any
	if err := json.Unmarshal(runner.calls[1].stdin, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	spec := manifest["spec"].(map[string]any)
	if spec["action"] != "partition" || spec["direction"] != "both" || spec["duration"] != "36s" {
		t.Fatalf("spec = %+v", spec)
	}
	wantWait := []string{"--namespace", request.Namespace, "wait", "--for=condition=AllInjected", "networkchaos/ethquake-fault-1-cl-partition", "--timeout=60s"}
	if !reflect.DeepEqual(runner.calls[2].arguments, wantWait) {
		t.Fatalf("wait arguments = %v", runner.calls[2].arguments)
	}
}

func TestChaosMeshApplyRevertsWhenInjectionWaitFails(t *testing.T) {
	runner := &fakeRunner{
		responses: [][]byte{ownedNamespaceJSON(), nil, nil, ownedNamespaceJSON(), ownedFaultJSON(), nil},
		errors:    []error{nil, nil, errors.New("not injected")},
	}
	backend := &ChaosMesh{runner: runner}
	err := backend.Apply(context.Background(), validRequest())
	if err == nil || !strings.Contains(err.Error(), "wait for NetworkChaos") {
		t.Fatalf("Apply() error = %v", err)
	}
	last := runner.calls[len(runner.calls)-1].arguments
	if len(last) < 3 || last[2] != "delete" {
		t.Fatalf("cleanup arguments = %v", last)
	}
}

func TestChaosMeshRevertIsIdempotent(t *testing.T) {
	runner := &fakeRunner{responses: [][]byte{ownedNamespaceJSON(), nil}}
	backend := &ChaosMesh{runner: runner}
	if err := backend.Revert(context.Background(), validRequest()); err != nil {
		t.Fatalf("Revert() error = %v", err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %d", len(runner.calls))
	}
}

func TestChaosMeshRejectsForeignNamespace(t *testing.T) {
	runner := &fakeRunner{responses: [][]byte{[]byte(`{"metadata":{"labels":{}}}`)}}
	backend := &ChaosMesh{runner: runner}
	err := backend.Apply(context.Background(), validRequest())
	if !errors.Is(err, ErrOwnership) {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %d", len(runner.calls))
	}
}

func TestChaosMeshRejectsOverlappingPods(t *testing.T) {
	runner := &fakeRunner{}
	backend := &ChaosMesh{runner: runner}
	request := validRequest()
	request.GroupB = append(request.GroupB, request.GroupA[0])
	err := backend.Apply(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "both partition groups") {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls = %d", len(runner.calls))
	}
}

func validRequest() PartitionRequest {
	return PartitionRequest{
		RunID:           "fault-1",
		Namespace:       "kt-ethquake-phase3-fault-1",
		NamespacePrefix: "kt-ethquake-phase3-",
		GroupA:          []string{"cl-lighthouse-a", "cl-teku-a"},
		GroupB:          []string{"cl-lighthouse-b", "cl-teku-b"},
		TTL:             36 * time.Second,
	}
}

func ownedNamespaceJSON() []byte {
	return []byte(`{"metadata":{"labels":{"dev.ethquake.managed":"true","dev.ethquake.phase":"3","dev.ethquake.run-id":"fault-1"}}}`)
}

func ownedFaultJSON() []byte {
	return []byte(`{"metadata":{"labels":{"dev.ethquake.managed":"true","dev.ethquake.phase":"3","dev.ethquake.run-id":"fault-1"}},"status":{"conditions":[{"type":"AllInjected","status":"True"}]}}`)
}

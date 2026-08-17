//go:build e2e

package fault

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	chaosSmokeContext   = "kind-ethquake-chaos-smoke"
	chaosSmokeNamespace = "kt-ethquake-phase3-local-smoke"
	chaosSmokeRunID     = "local-smoke"
	chaosSmokePodA      = "smoke-a"
	chaosSmokePodB      = "smoke-b"
)

func TestChaosMeshAgainstDisposableKind(t *testing.T) {
	kubectlPath := requireE2EEnvironment(t, "ETHQUAKE_CHAOS_E2E_KUBECTL")
	kubeconfigPath := requireE2EEnvironment(t, "ETHQUAKE_CHAOS_E2E_KUBECONFIG")
	contextName := requireE2EEnvironment(t, "ETHQUAKE_CHAOS_E2E_CONTEXT")
	if contextName != chaosSmokeContext {
		t.Fatalf("ETHQUAKE_CHAOS_E2E_CONTEXT = %q; want %q", contextName, chaosSmokeContext)
	}

	backend, err := NewChaosMesh(kubectlPath, kubeconfigPath, contextName)
	if err != nil {
		t.Fatalf("NewChaosMesh() error = %v", err)
	}
	request := PartitionRequest{
		RunID:           chaosSmokeRunID,
		Namespace:       chaosSmokeNamespace,
		NamespacePrefix: "kt-ethquake-phase3-",
		GroupA:          []string{chaosSmokePodA},
		GroupB:          []string{chaosSmokePodB},
		TTL:             15 * time.Second,
	}

	testContext, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if revertErr := backend.Revert(cleanupContext, request); revertErr != nil {
			t.Errorf("cleanup Revert() error = %v", revertErr)
		}
	})

	podBAddress := podIP(t, testContext, kubectlPath, kubeconfigPath, contextName, chaosSmokePodB)
	if output, err := probeHTTP(testContext, kubectlPath, kubeconfigPath, contextName, podBAddress); err != nil {
		t.Fatalf("baseline connectivity failed: %v: %s", err, output)
	}

	if err := backend.Apply(testContext, request); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	status, err := backend.Status(testContext, request)
	if err != nil {
		t.Fatalf("Status() after Apply error = %v", err)
	}
	if !status.Exists || !status.AllInjected {
		t.Fatalf("Status() after Apply = %+v", status)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		probeContext, probeCancel := context.WithTimeout(testContext, 4*time.Second)
		output, probeErr := probeHTTP(probeContext, kubectlPath, kubeconfigPath, contextName, podBAddress)
		probeCancel()
		if probeErr == nil {
			t.Fatalf("partition probe %d unexpectedly succeeded: %s", attempt, output)
		}
	}

	recoveryDeadline := time.Now().Add(45 * time.Second)
	for {
		probeContext, probeCancel := context.WithTimeout(testContext, 4*time.Second)
		_, probeErr := probeHTTP(probeContext, kubectlPath, kubeconfigPath, contextName, podBAddress)
		probeCancel()
		if probeErr == nil {
			break
		}
		if time.Now().After(recoveryDeadline) {
			t.Fatalf("connectivity did not recover after fault TTL: %v", probeErr)
		}
		select {
		case <-testContext.Done():
			t.Fatalf("wait for TTL recovery: %v", testContext.Err())
		case <-time.After(time.Second):
		}
	}

	statusDeadline := time.Now().Add(15 * time.Second)
	for {
		status, err = backend.Status(testContext, request)
		if err != nil {
			t.Fatalf("Status() after TTL error = %v", err)
		}
		if status.Exists && status.AllRecovered {
			break
		}
		if time.Now().After(statusDeadline) {
			t.Fatalf("Status() after TTL = %+v", status)
		}
		select {
		case <-testContext.Done():
			t.Fatalf("wait for recovered status: %v", testContext.Err())
		case <-time.After(time.Second):
		}
	}
	if err := backend.Revert(testContext, request); err != nil {
		t.Fatalf("first Revert() error = %v", err)
	}
	if err := backend.Revert(testContext, request); err != nil {
		t.Fatalf("idempotent Revert() error = %v", err)
	}
	status, err = backend.Status(testContext, request)
	if err != nil {
		t.Fatalf("Status() after Revert error = %v", err)
	}
	if status.Exists {
		t.Fatalf("Status() after Revert = %+v", status)
	}
}

func requireE2EEnvironment(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("%s is required", name)
	}
	return value
}

func podIP(t *testing.T, ctx context.Context, kubectlPath, kubeconfigPath, contextName, podName string) string {
	t.Helper()
	output, err := runKubectl(ctx, kubectlPath, kubeconfigPath, contextName,
		"--namespace", chaosSmokeNamespace,
		"get", "pod", podName,
		"--output=jsonpath={.status.podIP}",
	)
	if err != nil {
		t.Fatalf("read Pod %q address: %v: %s", podName, err, output)
	}
	address := strings.TrimSpace(output)
	if address == "" || strings.ContainsAny(address, " /:@") {
		t.Fatalf("Pod %q returned invalid address %q", podName, address)
	}
	return address
}

func probeHTTP(ctx context.Context, kubectlPath, kubeconfigPath, contextName, address string) (string, error) {
	return runKubectl(ctx, kubectlPath, kubeconfigPath, contextName,
		"--namespace", chaosSmokeNamespace,
		"exec", chaosSmokePodA, "--",
		"wget", "-qO-", "-T", "2", "-t", "1", fmt.Sprintf("http://%s/", address),
	)
}

func runKubectl(ctx context.Context, kubectlPath, kubeconfigPath, contextName string, arguments ...string) (string, error) {
	baseArguments := []string{
		"--kubeconfig", kubeconfigPath,
		"--context", contextName,
	}
	command := exec.CommandContext(ctx, kubectlPath, append(baseArguments, arguments...)...)
	output, err := command.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

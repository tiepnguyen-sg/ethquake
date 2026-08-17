//go:build e2e

package fault

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	chaosSmokeContext      = "kind-ethquake-chaos-smoke"
	chaosSmokeNamespace    = "kt-ethquake-phase3-local-smoke"
	chaosSmokeRunID        = "local-smoke"
	chaosSmokePodA         = "smoke-a"
	chaosSmokePodB         = "smoke-b"
	chaosDeadmanNamespace  = "kt-ethquake-phase3-local-deadman"
	chaosDeadmanRunID      = "local-deadman"
	chaosDeadmanPodA       = "deadman-a"
	chaosDeadmanPodB       = "deadman-b"
	chaosDeadmanReadyFile  = "deadman-ready"
	chaosDeadmanTTL        = 30 * time.Second
	chaosE2ERecoveryWindow = 45 * time.Second
)

type chaosE2EEnvironment struct {
	kubectlPath    string
	kubeconfigPath string
	contextName    string
}

func TestChaosMeshAgainstDisposableKind(t *testing.T) {
	environment, backend := newChaosE2EBackend(t)
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

	podBAddress := podIP(t, testContext, environment, chaosSmokeNamespace, chaosSmokePodB)
	if output, err := probeHTTP(testContext, environment, chaosSmokeNamespace, chaosSmokePodA, podBAddress); err != nil {
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
		output, probeErr := probeHTTP(probeContext, environment, chaosSmokeNamespace, chaosSmokePodA, podBAddress)
		probeCancel()
		if probeErr == nil {
			t.Fatalf("partition probe %d unexpectedly succeeded: %s", attempt, output)
		}
	}

	waitForConnectivityRecovery(t, testContext, environment, chaosSmokeNamespace, chaosSmokePodA, podBAddress)
	waitForRecoveredStatus(t, testContext, backend, request)
	requireIdempotentRevert(t, testContext, backend, request)
}

// TestChaosMeshDeadmanRunner is executed as a standalone test binary so the
// shell harness can kill the exact process that owns the fault operation.
func TestChaosMeshDeadmanRunner(t *testing.T) {
	_, backend := newChaosE2EBackend(t)
	request := deadmanRequest()
	runnerContext, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := backend.Apply(runnerContext, request); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	status, err := backend.Status(runnerContext, request)
	if err != nil {
		t.Fatalf("Status() after Apply error = %v", err)
	}
	if !status.Exists || !status.AllInjected {
		t.Fatalf("Status() after Apply = %+v", status)
	}
	signalDeadmanReady(t)

	<-runnerContext.Done()
	t.Fatalf("deadman runner was not killed before its safety deadline: %v", runnerContext.Err())
}

func TestChaosMeshDeadmanRecovery(t *testing.T) {
	environment, backend := newChaosE2EBackend(t)
	request := deadmanRequest()
	testContext, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if revertErr := backend.Revert(cleanupContext, request); revertErr != nil {
			t.Errorf("cleanup Revert() error = %v", revertErr)
		}
	})

	status, err := backend.Status(testContext, request)
	if err != nil {
		t.Fatalf("Status() after runner SIGKILL error = %v", err)
	}
	if !status.Exists || !status.AllInjected {
		t.Fatalf("Status() after runner SIGKILL = %+v", status)
	}
	podBAddress := podIP(t, testContext, environment, chaosDeadmanNamespace, chaosDeadmanPodB)
	for attempt := 1; attempt <= 2; attempt++ {
		probeContext, probeCancel := context.WithTimeout(testContext, 4*time.Second)
		output, probeErr := probeHTTP(
			probeContext,
			environment,
			chaosDeadmanNamespace,
			chaosDeadmanPodA,
			podBAddress,
		)
		probeCancel()
		if probeErr == nil {
			t.Fatalf("post-SIGKILL partition probe %d unexpectedly succeeded: %s", attempt, output)
		}
	}

	waitForConnectivityRecovery(t, testContext, environment, chaosDeadmanNamespace, chaosDeadmanPodA, podBAddress)
	waitForRecoveredStatus(t, testContext, backend, request)
	requireIdempotentRevert(t, testContext, backend, request)
}

func newChaosE2EBackend(t *testing.T) (chaosE2EEnvironment, *ChaosMesh) {
	t.Helper()
	environment := chaosE2EEnvironment{
		kubectlPath:    requireE2EEnvironment(t, "ETHQUAKE_CHAOS_E2E_KUBECTL"),
		kubeconfigPath: requireE2EEnvironment(t, "ETHQUAKE_CHAOS_E2E_KUBECONFIG"),
		contextName:    requireE2EEnvironment(t, "ETHQUAKE_CHAOS_E2E_CONTEXT"),
	}
	if environment.contextName != chaosSmokeContext {
		t.Fatalf("ETHQUAKE_CHAOS_E2E_CONTEXT = %q; want %q", environment.contextName, chaosSmokeContext)
	}
	backend, err := NewChaosMesh(environment.kubectlPath, environment.kubeconfigPath, environment.contextName)
	if err != nil {
		t.Fatalf("NewChaosMesh() error = %v", err)
	}
	return environment, backend
}

func deadmanRequest() PartitionRequest {
	return PartitionRequest{
		RunID:           chaosDeadmanRunID,
		Namespace:       chaosDeadmanNamespace,
		NamespacePrefix: "kt-ethquake-phase3-",
		GroupA:          []string{chaosDeadmanPodA},
		GroupB:          []string{chaosDeadmanPodB},
		TTL:             chaosDeadmanTTL,
	}
}

func signalDeadmanReady(t *testing.T) {
	t.Helper()
	path := requireE2EEnvironment(t, "ETHQUAKE_CHAOS_E2E_READY_FILE")
	if !filepath.IsAbs(path) || filepath.Base(path) != chaosDeadmanReadyFile {
		t.Fatalf("ETHQUAKE_CHAOS_E2E_READY_FILE = %q; want an absolute %q path", path, chaosDeadmanReadyFile)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("create deadman readiness file: %v", err)
	}
	if _, err := file.WriteString("ready\n"); err != nil {
		_ = file.Close()
		t.Fatalf("write deadman readiness file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close deadman readiness file: %v", err)
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

func podIP(
	t *testing.T,
	ctx context.Context,
	environment chaosE2EEnvironment,
	namespace string,
	podName string,
) string {
	t.Helper()
	output, err := runKubectl(ctx, environment,
		"--namespace", namespace,
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

func probeHTTP(
	ctx context.Context,
	environment chaosE2EEnvironment,
	namespace string,
	sourcePod string,
	address string,
) (string, error) {
	return runKubectl(ctx, environment,
		"--namespace", namespace,
		"exec", sourcePod, "--",
		"wget", "-qO-", "-T", "2", "-t", "1", fmt.Sprintf("http://%s/", address),
	)
}

func waitForConnectivityRecovery(
	t *testing.T,
	ctx context.Context,
	environment chaosE2EEnvironment,
	namespace string,
	sourcePod string,
	address string,
) {
	t.Helper()
	recoveryDeadline := time.Now().Add(chaosE2ERecoveryWindow)
	for {
		probeContext, probeCancel := context.WithTimeout(ctx, 4*time.Second)
		_, probeErr := probeHTTP(probeContext, environment, namespace, sourcePod, address)
		probeCancel()
		if probeErr == nil {
			return
		}
		if time.Now().After(recoveryDeadline) {
			t.Fatalf("connectivity did not recover after fault TTL: %v", probeErr)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for TTL recovery: %v", ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

func waitForRecoveredStatus(t *testing.T, ctx context.Context, backend *ChaosMesh, request PartitionRequest) {
	t.Helper()
	statusDeadline := time.Now().Add(15 * time.Second)
	for {
		status, err := backend.Status(ctx, request)
		if err != nil {
			t.Fatalf("Status() after TTL error = %v", err)
		}
		if status.Exists && status.AllRecovered {
			return
		}
		if time.Now().After(statusDeadline) {
			t.Fatalf("Status() after TTL = %+v", status)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for recovered status: %v", ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

func requireIdempotentRevert(
	t *testing.T,
	ctx context.Context,
	backend *ChaosMesh,
	request PartitionRequest,
) {
	t.Helper()
	if err := backend.Revert(ctx, request); err != nil {
		t.Fatalf("first Revert() error = %v", err)
	}
	if err := backend.Revert(ctx, request); err != nil {
		t.Fatalf("idempotent Revert() error = %v", err)
	}
	status, err := backend.Status(ctx, request)
	if err != nil {
		t.Fatalf("Status() after Revert error = %v", err)
	}
	if status.Exists {
		t.Fatalf("Status() after Revert = %+v", status)
	}
}

func runKubectl(ctx context.Context, environment chaosE2EEnvironment, arguments ...string) (string, error) {
	baseArguments := []string{
		"--kubeconfig", environment.kubeconfigPath,
		"--context", environment.contextName,
	}
	command := exec.CommandContext(ctx, environment.kubectlPath, append(baseArguments, arguments...)...)
	output, err := command.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}

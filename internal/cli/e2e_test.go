//go:build e2e

package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestObserverAgainstLiveDevnet(t *testing.T) {
	beaconOne := requiredEnvironment(t, "ETHQUAKE_E2E_BEACON_ONE")
	beaconTwo := requiredEnvironment(t, "ETHQUAKE_E2E_BEACON_TWO")
	executionOne := requiredEnvironment(t, "ETHQUAKE_E2E_EXECUTION_ONE")
	executionTwo := requiredEnvironment(t, "ETHQUAKE_E2E_EXECUTION_TWO")

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	output := &e2eObservationWriter{cancel: cancel}
	var stderr bytes.Buffer
	err := Run(ctx, []string{
		"observe",
		"--beacon", beaconOne,
		"--beacon", beaconTwo,
		"--execution", executionOne,
		"--execution", executionTwo,
		"--metrics-address", "127.0.0.1:0",
		"--poll-interval", "1s",
		"--request-timeout", "3s",
	}, output, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v; stderr = %s", err, stderr.String())
	}

	contents := output.String()
	if strings.Contains(contents, `"type":"measurement_gap"`) {
		t.Fatalf("live observation contains a measurement gap: %s", contents)
	}
	if !strings.Contains(contents, `"type":"head_comparison"`) {
		t.Fatalf("live observation lacks a head comparison: %s", contents)
	}
	if !strings.Contains(contents, `"target":"geth","execution_head"`) ||
		!strings.Contains(contents, `"target":"reth","execution_head"`) {
		t.Fatalf("live observation lacks both execution targets: %s", contents)
	}
}

type e2eObservationWriter struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	cancel context.CancelFunc
	once   sync.Once
}

func (w *e2eObservationWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	written, err := w.buffer.Write(value)
	contents := w.buffer.String()
	complete := strings.Contains(contents, `"type":"head_comparison"`) &&
		strings.Contains(contents, `"target":"geth","execution_head"`) &&
		strings.Contains(contents, `"target":"reth","execution_head"`)
	w.mu.Unlock()
	if complete {
		w.once.Do(w.cancel)
	}
	return written, err
}

func (w *e2eObservationWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

func requiredEnvironment(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("required environment variable is missing: %s", name)
	}
	return value
}

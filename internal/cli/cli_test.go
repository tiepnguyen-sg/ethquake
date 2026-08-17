package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestObserveRunsAgainstEndpointFlagsOnly(t *testing.T) {
	server := newBeaconFixtureServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	output := &cancelWriter{cancel: cancel, match: `"type":"beacon_observation"`}
	var stderr bytes.Buffer

	err := Run(ctx, []string{
		"observe",
		"--beacon", "independent=" + server.URL,
		"--metrics-address", "127.0.0.1:0",
		"--poll-interval", "1h",
		"--request-timeout", "1s",
	}, output, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v; stderr = %s", err, stderr.String())
	}

	contents := output.String()
	if !strings.Contains(contents, `"type":"runtime_spec"`) {
		t.Fatalf("output lacks runtime spec: %s", contents)
	}
	if !strings.Contains(contents, `"type":"beacon_observation"`) {
		t.Fatalf("output lacks Beacon observation: %s", contents)
	}
	if !strings.Contains(contents, `"finality_lag_slots":72`) {
		t.Fatalf("output lacks calculated finality lag: %s", contents)
	}
}

func TestObserveAcceptsExecutionEndpointWithoutTopologyConfiguration(t *testing.T) {
	beaconServer := newBeaconFixtureServer(t)
	executionServer := newExecutionFixtureServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	output := &cancelWriter{cancel: cancel, match: `"type":"execution_head"`}
	var stderr bytes.Buffer

	err := Run(ctx, []string{
		"observe",
		"--beacon", "node=" + beaconServer.URL,
		"--execution", "node=ws" + strings.TrimPrefix(executionServer.URL, "http"),
		"--metrics-address", "127.0.0.1:0",
		"--poll-interval", "1h",
		"--request-timeout", "1s",
	}, output, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v; stderr = %s", err, stderr.String())
	}
	contents := output.String()
	if !strings.Contains(contents, `"type":"execution_head"`) || !strings.Contains(contents, `"continuity":true`) {
		t.Fatalf("output lacks execution observation: %s", contents)
	}
}

func TestObserveRejectsNonLoopbackMetricsListener(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{
		"observe",
		"--beacon", "node=http://127.0.0.1:5052",
		"--metrics-address", "0.0.0.0:9464",
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "not a literal loopback") {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestObserveRejectsDuplicateEndpointNames(t *testing.T) {
	var stdout, stderr bytes.Buffer
	outputPath := filepath.Join(t.TempDir(), "must-not-exist.jsonl")
	err := Run(context.Background(), []string{
		"observe",
		"--beacon", "node=http://127.0.0.1:5052",
		"--beacon", "node=http://127.0.0.1:5053",
		"--metrics-address", "127.0.0.1:0",
		"--output", outputPath,
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("Run() error = %v", err)
	}
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Fatalf("invalid configuration created output file; stat error = %v", statErr)
	}
}

func TestOpenOutputDoesNotOverwriteExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observations.jsonl")
	if err := os.WriteFile(path, []byte("existing evidence"), 0o600); err != nil {
		t.Fatalf("prepare output: %v", err)
	}
	if _, _, err := openOutput(path, &bytes.Buffer{}); err == nil {
		t.Fatal("openOutput() error = nil")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read existing output: %v", err)
	}
	if string(contents) != "existing evidence" {
		t.Fatalf("existing output changed to %q", contents)
	}
}

func TestValidateLoopbackAddress(t *testing.T) {
	valid := []string{"127.0.0.1:9464", "[::1]:9464", "127.0.0.1:0"}
	for _, address := range valid {
		if err := validateLoopbackAddress(address); err != nil {
			t.Errorf("validateLoopbackAddress(%q) error = %v", address, err)
		}
	}

	invalid := []string{"localhost:9464", "0.0.0.0:9464", "[::]:9464", "127.0.0.1", "127.0.0.1:not-a-port"}
	for _, address := range invalid {
		if err := validateLoopbackAddress(address); err == nil {
			t.Errorf("validateLoopbackAddress(%q) error = nil", address)
		}
	}
}

type cancelWriter struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	cancel context.CancelFunc
	match  string
	once   sync.Once
}

func (w *cancelWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	written, err := w.buffer.Write(value)
	shouldCancel := strings.Contains(w.buffer.String(), w.match)
	w.mu.Unlock()
	if shouldCancel {
		w.once.Do(w.cancel)
	}
	return written, err
}

func newExecutionFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			t.Errorf("accept execution WebSocket: %v", err)
			return
		}
		defer connection.CloseNow()
		if _, _, err := connection.Read(request.Context()); err != nil {
			t.Errorf("read execution subscription: %v", err)
			return
		}
		if err := connection.Write(request.Context(), websocket.MessageText, []byte(
			`{"jsonrpc":"2.0","id":1,"result":"0xsubscription"}`,
		)); err != nil {
			t.Errorf("write execution subscription response: %v", err)
			return
		}
		if err := connection.Write(request.Context(), websocket.MessageText, []byte(
			`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"0xsubscription","result":{"number":"0xa8","hash":"0x1111111111111111111111111111111111111111111111111111111111111111","parentHash":"0x2222222222222222222222222222222222222222222222222222222222222222"}}}`,
		)); err != nil {
			t.Errorf("write execution head: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func (w *cancelWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buffer.String()
}

func newBeaconFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	genesisTime := time.Now().Unix() - 168*12
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/eth/v1/config/spec":
			_, _ = response.Write([]byte(`{"data":{"SECONDS_PER_SLOT":"12","SLOTS_PER_EPOCH":"32","DENEB_FORK_EPOCH":"0"}}`))
		case "/eth/v1/beacon/genesis":
			_, _ = fmt.Fprintf(response, `{"data":{"genesis_time":"%d","genesis_validators_root":"0x4444444444444444444444444444444444444444444444444444444444444444","genesis_fork_version":"0x10000038"}}`, genesisTime)
		case "/eth/v1/beacon/headers/genesis":
			_, _ = response.Write([]byte(`{"data":{"canonical":true,"header":{"message":{"slot":"0"}}}}`))
		case "/eth/v1/beacon/headers/head":
			_, _ = response.Write([]byte(`{"execution_optimistic":false,"data":{"root":"0x1111111111111111111111111111111111111111111111111111111111111111","canonical":true,"header":{"message":{"slot":"168","parent_root":"0x2222222222222222222222222222222222222222222222222222222222222222","state_root":"0x5555555555555555555555555555555555555555555555555555555555555555"}}}}`))
		case "/eth/v1/beacon/states/0x5555555555555555555555555555555555555555555555555555555555555555/finality_checkpoints":
			_, _ = response.Write([]byte(`{"data":{"finalized":{"epoch":"3","root":"0x3333333333333333333333333333333333333333333333333333333333333333"}}}`))
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestObserveHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := newBeaconFixtureServer(t)
	var stdout, stderr bytes.Buffer
	started := time.Now()
	err := Run(ctx, []string{
		"observe",
		"--beacon", "node=" + server.URL,
		"--metrics-address", "127.0.0.1:0",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("Run() did not stop promptly after cancellation")
	}
}

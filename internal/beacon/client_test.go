package beacon

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestClientReadsRecordedFixtures(t *testing.T) {
	fixtures := map[string][]byte{
		"/eth/v1/config/spec":                             readFixture(t, "spec.json"),
		"/eth/v1/beacon/headers/head":                     readFixture(t, "head.json"),
		"/eth/v1/beacon/states/head/finality_checkpoints": readFixture(t, "finality.json"),
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, exists := fixtures[request.URL.Path]
		if !exists {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write(body)
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(server.URL, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	spec, err := client.Spec(context.Background())
	if err != nil {
		t.Fatalf("Spec() error = %v", err)
	}
	wantSpec := Spec{
		SecondsPerSlot: 12,
		SlotsPerEpoch:  32,
		ForkEpochs:     map[string]string{"DENEB_FORK_EPOCH": "0"},
	}
	if !maps.Equal(spec.ForkEpochs, wantSpec.ForkEpochs) ||
		spec.SecondsPerSlot != wantSpec.SecondsPerSlot ||
		spec.SlotsPerEpoch != wantSpec.SlotsPerEpoch {
		t.Fatalf("Spec() = %+v", spec)
	}

	head, err := client.Head(context.Background())
	if err != nil {
		t.Fatalf("Head() error = %v", err)
	}
	if head.Slot != 168 || head.Root != "0x90c8b7ff441b3aa4ae69026fb3a2265b9c5772118c5ea84ab1d48860006d49fc" || !head.Canonical || head.ExecutionOptimistic {
		t.Fatalf("Head() = %+v", head)
	}

	finality, err := client.Finality(context.Background())
	if err != nil {
		t.Fatalf("Finality() error = %v", err)
	}
	if finality.Epoch != 3 || finality.Root != "0xe82f6634ae3efadc4aefe87761efe0a5df03dc5ca30eff1b372b30a2cdf2b44b" {
		t.Fatalf("Finality() = %+v", finality)
	}
}

func TestSpecCompatibilityAllowsAdditionalFutureForks(t *testing.T) {
	lighthouse := Spec{
		SecondsPerSlot: 12,
		SlotsPerEpoch:  32,
		ForkEpochs:     map[string]string{"GLOAS_FORK_EPOCH": "18446744073709551615"},
	}
	teku := Spec{
		SecondsPerSlot: 12,
		SlotsPerEpoch:  32,
		ForkEpochs: map[string]string{
			"GLOAS_FORK_EPOCH": "18446744073709551615",
			"HEZE_FORK_EPOCH":  "18446744073709551615",
		},
	}
	if !lighthouse.Compatible(teku) || !teku.Compatible(lighthouse) {
		t.Fatal("additional client-specific future fork should remain compatible")
	}
	teku.ForkEpochs["GLOAS_FORK_EPOCH"] = "10"
	if lighthouse.Compatible(teku) || teku.Compatible(lighthouse) {
		t.Fatal("conflicting shared fork epoch should be incompatible")
	}
}

func TestClientRejectsNonCanonicalHead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{"execution_optimistic":false,"data":{"root":"0x1111111111111111111111111111111111111111111111111111111111111111","canonical":false,"header":{"message":{"slot":"1","parent_root":"0x2222222222222222222222222222222222222222222222222222222222222222"}}}}`))
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Head(context.Background()); err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("Head() error = %v", err)
	}
}

func TestNewClientRejectsUnsafeOrInvalidEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		timeout  time.Duration
	}{
		{name: "missing scheme", endpoint: "127.0.0.1:5052", timeout: time.Second},
		{name: "unsupported scheme", endpoint: "file:///tmp/beacon", timeout: time.Second},
		{name: "embedded credentials", endpoint: "https://user:secret@example.test", timeout: time.Second},
		{name: "query", endpoint: "https://example.test?token=secret", timeout: time.Second},
		{name: "zero timeout", endpoint: "https://example.test", timeout: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewClient(test.endpoint, test.timeout); err == nil {
				t.Fatal("NewClient() error = nil")
			}
		})
	}
}

func TestClientRejectsMalformedResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid JSON", body: "{"},
		{name: "missing constant", body: `{"data":{"SECONDS_PER_SLOT":"12"}}`},
		{name: "zero constant", body: `{"data":{"SECONDS_PER_SLOT":"0","SLOTS_PER_EPOCH":"32"}}`},
		{name: "non-decimal constant", body: `{"data":{"SECONDS_PER_SLOT":"twelve","SLOTS_PER_EPOCH":"32"}}`},
		{name: "missing fork epochs", body: `{"data":{"SECONDS_PER_SLOT":"12","SLOTS_PER_EPOCH":"32"}}`},
		{name: "invalid fork epoch", body: `{"data":{"SECONDS_PER_SLOT":"12","SLOTS_PER_EPOCH":"32","DENEB_FORK_EPOCH":"never"}}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				_, _ = response.Write([]byte(test.body))
			}))
			t.Cleanup(server.Close)
			client, err := NewClient(server.URL, time.Second)
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			if _, err := client.Spec(context.Background()); err == nil {
				t.Fatal("Spec() error = nil")
			}
		})
	}
}

func TestClientRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(strings.Repeat("x", maxResponseBytes+1)))
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Spec(context.Background()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Spec() error = %v", err)
	}
}

func TestClientPropagatesCancellation(t *testing.T) {
	requestStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, callErr := client.Spec(ctx)
		result <- callErr
	}()
	<-requestStarted
	cancel()

	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Spec() error = %v, want context.Canceled", err)
	}
}

func TestClientReportsHTTPStatusWithoutResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "sensitive upstream detail", http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(server.URL, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	_, err = client.Spec(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("Spec() error = %v", err)
	}
	if strings.Contains(err.Error(), "sensitive upstream detail") {
		t.Fatalf("Spec() leaked response body: %v", err)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	contents, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return contents
}

package execution

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestSubscribeNewHeadsReadsValidatedNotification(t *testing.T) {
	requestSeen := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			t.Errorf("accept WebSocket: %v", err)
			return
		}
		defer connection.CloseNow()
		_, requestBody, err := connection.Read(request.Context())
		if err != nil {
			t.Errorf("read subscription request: %v", err)
			return
		}
		requestSeen <- string(requestBody)
		if err := connection.Write(request.Context(), websocket.MessageText, []byte(
			`{"jsonrpc":"2.0","id":1,"result":"0xsubscription"}`,
		)); err != nil {
			t.Errorf("write subscription response: %v", err)
			return
		}
		if err := connection.Write(request.Context(), websocket.MessageText, []byte(
			`{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"0xsubscription","result":{"number":"0xa8","hash":"0x1111111111111111111111111111111111111111111111111111111111111111","parentHash":"0x2222222222222222222222222222222222222222222222222222222222222222"}}}`,
		)); err != nil {
			t.Errorf("write head notification: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewClient("ws"+strings.TrimPrefix(server.URL, "http"), time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	stop := errors.New("stop after first head")
	var got Head
	err = client.SubscribeNewHeads(context.Background(), func(head Head) error {
		got = head
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("SubscribeNewHeads() error = %v, want sentinel", err)
	}
	if got != (Head{Number: 168, Hash: hash("1"), ParentHash: hash("2")}) {
		t.Fatalf("received head = %+v", got)
	}
	if request := <-requestSeen; request != `{"jsonrpc":"2.0","id":1,"method":"eth_subscribe","params":["newHeads"]}` {
		t.Fatalf("subscription request = %s", request)
	}
}

func TestChainID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			t.Errorf("accept WebSocket: %v", err)
			return
		}
		defer connection.CloseNow()
		_, body, err := connection.Read(request.Context())
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		if string(body) != `{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}` {
			t.Errorf("request = %s", body)
			return
		}
		_ = connection.Write(request.Context(), websocket.MessageText, []byte(`{"jsonrpc":"2.0","id":1,"result":"0x301824"}`))
	}))
	t.Cleanup(server.Close)
	client, err := NewClient("ws"+strings.TrimPrefix(server.URL, "http"), time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	chainID, err := client.ChainID(context.Background())
	if err != nil || chainID != 3151908 {
		t.Fatalf("ChainID() = %d, %v", chainID, err)
	}
}

func TestNewClientRejectsInvalidEndpoints(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		timeout  time.Duration
	}{
		{name: "HTTP scheme", endpoint: "http://example.test", timeout: time.Second},
		{name: "missing host", endpoint: "ws:///socket", timeout: time.Second},
		{name: "embedded credentials", endpoint: "wss://user:secret@example.test", timeout: time.Second},
		{name: "query", endpoint: "wss://example.test?token=secret", timeout: time.Second},
		{name: "zero timeout", endpoint: "wss://example.test", timeout: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewClient(test.endpoint, test.timeout); err == nil {
				t.Fatal("NewClient() error = nil")
			}
		})
	}
}

func TestParseNotificationRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{name: "invalid JSON", message: "{"},
		{name: "wrong method", message: `{"jsonrpc":"2.0","method":"other","params":{"subscription":"0x1"}}`},
		{name: "leading-zero number", message: notificationWithNumber("0x01")},
		{name: "empty number", message: notificationWithNumber("0x")},
		{name: "invalid hash", message: `{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"0x1","result":{"number":"0x1","hash":"0x01","parentHash":"0x2222222222222222222222222222222222222222222222222222222222222222"}}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := parseNotification([]byte(test.message)); err == nil {
				t.Fatal("parseNotification() error = nil")
			}
		})
	}
}

func TestSubscribeNewHeadsReportsRejectedSubscriptionWithoutLeakingDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		connection, err := websocket.Accept(response, request, nil)
		if err != nil {
			t.Errorf("accept WebSocket: %v", err)
			return
		}
		defer connection.CloseNow()
		if _, _, err := connection.Read(request.Context()); err != nil {
			t.Errorf("read subscription request: %v", err)
			return
		}
		_ = connection.Write(request.Context(), websocket.MessageText, []byte(
			`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"sensitive node detail"}}`,
		))
	}))
	t.Cleanup(server.Close)
	client, err := NewClient("ws"+strings.TrimPrefix(server.URL, "http"), time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	err = client.SubscribeNewHeads(context.Background(), func(Head) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("SubscribeNewHeads() error = %v", err)
	}
	if strings.Contains(err.Error(), "sensitive node detail") {
		t.Fatalf("SubscribeNewHeads() leaked server detail: %v", err)
	}
}

func notificationWithNumber(number string) string {
	return `{"jsonrpc":"2.0","method":"eth_subscription","params":{"subscription":"0x1","result":{"number":"` + number + `","hash":"` + hash("1") + `","parentHash":"` + hash("2") + `"}}}`
}

func hash(character string) string {
	return "0x" + strings.Repeat(character, 64)
}

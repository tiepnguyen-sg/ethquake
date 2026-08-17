// Package execution reads measurements from Ethereum execution JSON-RPC.
package execution

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

const maxMessageBytes = 1 << 20

type Head struct {
	Number     uint64 `json:"number"`
	Hash       string `json:"hash"`
	ParentHash string `json:"parent_hash"`
}

type Client struct {
	endpoint         *url.URL
	handshakeTimeout time.Duration
}

func (c *Client) ChainID(ctx context.Context) (uint64, error) {
	handshakeContext, cancelHandshake := context.WithTimeout(ctx, c.handshakeTimeout)
	defer cancelHandshake()
	connection, response, err := websocket.Dial(handshakeContext, c.endpoint.String(), nil)
	if err != nil {
		if response != nil {
			return 0, fmt.Errorf("connect to execution WebSocket for chain ID: HTTP %s: %w", response.Status, err)
		}
		return 0, fmt.Errorf("connect to execution WebSocket for chain ID: %w", err)
	}
	defer connection.CloseNow()
	connection.SetReadLimit(maxMessageBytes)

	request := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      uint64 `json:"id"`
		Method  string `json:"method"`
		Params  []any  `json:"params"`
	}{JSONRPC: "2.0", ID: 1, Method: "eth_chainId", Params: []any{}}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return 0, fmt.Errorf("encode execution chain ID request: %w", err)
	}
	if err := connection.Write(handshakeContext, websocket.MessageText, requestBytes); err != nil {
		return 0, fmt.Errorf("request execution chain ID: %w", err)
	}
	_, message, err := connection.Read(handshakeContext)
	if err != nil {
		return 0, fmt.Errorf("read execution chain ID: %w", err)
	}
	var rpcResponse struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      uint64          `json:"id"`
		Result  string          `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(message, &rpcResponse); err != nil {
		return 0, fmt.Errorf("decode execution chain ID: %w", err)
	}
	if len(rpcResponse.Error) != 0 && string(rpcResponse.Error) != "null" {
		return 0, errors.New("execution client rejected eth_chainId")
	}
	if rpcResponse.JSONRPC != "2.0" || rpcResponse.ID != 1 {
		return 0, errors.New("execution client returned an invalid eth_chainId response")
	}
	chainID, err := parseQuantity("chain ID", rpcResponse.Result)
	if err != nil {
		return 0, err
	}
	return chainID, nil
}

func NewClient(rawEndpoint string, handshakeTimeout time.Duration) (*Client, error) {
	if handshakeTimeout <= 0 {
		return nil, errors.New("WebSocket handshake timeout must be positive")
	}
	endpoint, err := url.Parse(rawEndpoint)
	if err != nil {
		return nil, fmt.Errorf("parse execution WebSocket endpoint: %w", err)
	}
	if endpoint.Scheme != "ws" && endpoint.Scheme != "wss" {
		return nil, fmt.Errorf("execution endpoint scheme %q is not ws or wss", endpoint.Scheme)
	}
	if endpoint.Host == "" {
		return nil, errors.New("execution WebSocket endpoint must include a host")
	}
	if endpoint.User != nil {
		return nil, errors.New("execution WebSocket endpoint must not contain user information")
	}
	if endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("execution WebSocket endpoint must not contain a query or fragment")
	}

	return &Client{
		endpoint:         endpoint,
		handshakeTimeout: handshakeTimeout,
	}, nil
}

func (c *Client) SubscribeNewHeads(ctx context.Context, receive func(Head) error) (returnErr error) {
	if receive == nil {
		return errors.New("newHeads receiver is required")
	}

	handshakeContext, cancelHandshake := context.WithTimeout(ctx, c.handshakeTimeout)
	defer cancelHandshake()
	connection, response, err := websocket.Dial(handshakeContext, c.endpoint.String(), nil)
	if err != nil {
		if response != nil {
			return fmt.Errorf("connect to execution WebSocket: HTTP %s: %w", response.Status, err)
		}
		return fmt.Errorf("connect to execution WebSocket: %w", err)
	}
	defer func() {
		if err := connection.Close(websocket.StatusNormalClosure, "subscription closed"); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close execution WebSocket: %w", err))
		}
	}()
	connection.SetReadLimit(maxMessageBytes)

	request := struct {
		JSONRPC string   `json:"jsonrpc"`
		ID      uint64   `json:"id"`
		Method  string   `json:"method"`
		Params  []string `json:"params"`
	}{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "eth_subscribe",
		Params:  []string{"newHeads"},
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode newHeads subscription: %w", err)
	}
	if err := connection.Write(handshakeContext, websocket.MessageText, requestBytes); err != nil {
		return fmt.Errorf("send newHeads subscription: %w", err)
	}

	subscriptionID, err := readSubscriptionResponse(handshakeContext, connection)
	if err != nil {
		return err
	}
	cancelHandshake()
	for {
		_, message, err := connection.Read(ctx)
		if err != nil {
			return fmt.Errorf("read newHeads notification: %w", err)
		}
		head, notificationSubscription, err := parseNotification(message)
		if err != nil {
			return err
		}
		if notificationSubscription != subscriptionID {
			return fmt.Errorf("newHeads notification subscription %q does not match %q", notificationSubscription, subscriptionID)
		}
		if err := receive(head); err != nil {
			return fmt.Errorf("process newHeads notification: %w", err)
		}
	}
}

func readSubscriptionResponse(ctx context.Context, connection *websocket.Conn) (string, error) {
	_, message, err := connection.Read(ctx)
	if err != nil {
		return "", fmt.Errorf("read newHeads subscription response: %w", err)
	}
	var response struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      uint64          `json:"id"`
		Result  string          `json:"result"`
		Error   json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(message, &response); err != nil {
		return "", fmt.Errorf("decode newHeads subscription response: %w", err)
	}
	if len(response.Error) != 0 && string(response.Error) != "null" {
		return "", errors.New("execution client rejected newHeads subscription")
	}
	if response.JSONRPC != "2.0" || response.ID != 1 || response.Result == "" {
		return "", errors.New("execution client returned an invalid newHeads subscription response")
	}
	return response.Result, nil
}

func parseNotification(message []byte) (Head, string, error) {
	var notification struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  struct {
			Subscription string `json:"subscription"`
			Result       struct {
				Number     string `json:"number"`
				Hash       string `json:"hash"`
				ParentHash string `json:"parentHash"`
			} `json:"result"`
		} `json:"params"`
	}
	if err := json.Unmarshal(message, &notification); err != nil {
		return Head{}, "", fmt.Errorf("decode newHeads notification: %w", err)
	}
	if notification.JSONRPC != "2.0" || notification.Method != "eth_subscription" || notification.Params.Subscription == "" {
		return Head{}, "", errors.New("execution client returned an invalid newHeads notification")
	}
	number, err := parseQuantity("head number", notification.Params.Result.Number)
	if err != nil {
		return Head{}, "", err
	}
	hash, err := parseHash("head hash", notification.Params.Result.Hash)
	if err != nil {
		return Head{}, "", err
	}
	parentHash, err := parseHash("head parent hash", notification.Params.Result.ParentHash)
	if err != nil {
		return Head{}, "", err
	}
	return Head{Number: number, Hash: hash, ParentHash: parentHash}, notification.Params.Subscription, nil
}

func parseQuantity(name, value string) (uint64, error) {
	if !strings.HasPrefix(value, "0x") || len(value) < 3 {
		return 0, fmt.Errorf("%s must be a non-empty 0x-prefixed quantity", name)
	}
	digits := value[2:]
	if len(digits) > 1 && digits[0] == '0' {
		return 0, fmt.Errorf("%s %q has a leading zero", name, value)
	}
	parsed, err := strconv.ParseUint(digits, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s %q: %w", name, value, err)
	}
	return parsed, nil
}

func parseHash(name, value string) (string, error) {
	if len(value) != 66 || !strings.HasPrefix(value, "0x") {
		return "", fmt.Errorf("%s must be a 32-byte 0x-prefixed hex value", name)
	}
	if _, err := hex.DecodeString(value[2:]); err != nil {
		return "", fmt.Errorf("parse %s: %w", name, err)
	}
	return strings.ToLower(value), nil
}

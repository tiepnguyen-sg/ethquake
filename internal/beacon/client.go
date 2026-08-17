// Package beacon reads measurements from the standard Ethereum Beacon REST API.
package beacon

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxResponseBytes = 1 << 20

type Spec struct {
	SecondsPerSlot uint64            `json:"seconds_per_slot"`
	SlotsPerEpoch  uint64            `json:"slots_per_epoch"`
	ForkEpochs     map[string]string `json:"fork_epochs"`
}

type Genesis struct {
	Time           uint64 `json:"time"`
	Slot           uint64 `json:"slot"`
	ValidatorsRoot string `json:"validators_root"`
	ForkVersion    string `json:"fork_version"`
}

func (g Genesis) Compatible(other Genesis) bool {
	return g == other
}

func (s Spec) Compatible(other Spec) bool {
	if s.SecondsPerSlot != other.SecondsPerSlot || s.SlotsPerEpoch != other.SlotsPerEpoch {
		return false
	}
	for name, epoch := range s.ForkEpochs {
		if otherEpoch, exists := other.ForkEpochs[name]; exists && otherEpoch != epoch {
			return false
		}
	}
	return true
}

type Head struct {
	Slot                uint64 `json:"slot"`
	Root                string `json:"root"`
	ParentRoot          string `json:"parent_root"`
	StateRoot           string `json:"state_root"`
	Canonical           bool   `json:"canonical"`
	ExecutionOptimistic bool   `json:"execution_optimistic"`
}

type Finality struct {
	Epoch               uint64 `json:"epoch"`
	Root                string `json:"root"`
	ExecutionOptimistic bool   `json:"execution_optimistic"`
}

type Validator struct {
	Index            uint64 `json:"index"`
	EffectiveBalance uint64 `json:"effective_balance"`
	Status           string `json:"status"`
}

type Client struct {
	base       *url.URL
	httpClient *http.Client
}

func NewClient(rawEndpoint string, timeout time.Duration) (*Client, error) {
	if timeout <= 0 {
		return nil, errors.New("request timeout must be positive")
	}

	base, err := url.Parse(rawEndpoint)
	if err != nil {
		return nil, fmt.Errorf("parse Beacon API endpoint: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("Beacon API endpoint scheme %q is not http or https", base.Scheme)
	}
	if base.Host == "" {
		return nil, errors.New("Beacon API endpoint must include a host")
	}
	if base.User != nil {
		return nil, errors.New("Beacon API endpoint must not contain user information")
	}
	if base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("Beacon API endpoint must not contain a query or fragment")
	}

	return &Client{
		base:       base,
		httpClient: &http.Client{Timeout: timeout},
	}, nil
}

func (c *Client) Spec(ctx context.Context) (Spec, error) {
	var response struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := c.getJSON(ctx, "/eth/v1/config/spec", &response); err != nil {
		return Spec{}, fmt.Errorf("fetch runtime spec: %w", err)
	}

	secondsPerSlot, err := requiredDecimalField(response.Data, "SECONDS_PER_SLOT")
	if err != nil {
		return Spec{}, fmt.Errorf("parse runtime spec: %w", err)
	}
	slotsPerEpoch, err := requiredDecimalField(response.Data, "SLOTS_PER_EPOCH")
	if err != nil {
		return Spec{}, fmt.Errorf("parse runtime spec: %w", err)
	}
	if secondsPerSlot == 0 || slotsPerEpoch == 0 {
		return Spec{}, errors.New("parse runtime spec: SECONDS_PER_SLOT and SLOTS_PER_EPOCH must be positive")
	}
	forkEpochs := make(map[string]string)
	for name, raw := range response.Data {
		if !strings.HasSuffix(name, "_FORK_EPOCH") {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return Spec{}, fmt.Errorf("parse runtime spec: field %s is not a decimal string: %w", name, err)
		}
		parsedEpoch, err := parseDecimal(name, value)
		if err != nil {
			return Spec{}, fmt.Errorf("parse runtime spec: %w", err)
		}
		forkEpochs[name] = strconv.FormatUint(parsedEpoch, 10)
	}
	if len(forkEpochs) == 0 {
		return Spec{}, errors.New("parse runtime spec: no fork epochs were returned")
	}

	return Spec{
		SecondsPerSlot: secondsPerSlot,
		SlotsPerEpoch:  slotsPerEpoch,
		ForkEpochs:     forkEpochs,
	}, nil
}

func (c *Client) Genesis(ctx context.Context) (Genesis, error) {
	var response struct {
		Data struct {
			GenesisTime           string `json:"genesis_time"`
			GenesisValidatorsRoot string `json:"genesis_validators_root"`
			GenesisForkVersion    string `json:"genesis_fork_version"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, "/eth/v1/beacon/genesis", &response); err != nil {
		return Genesis{}, fmt.Errorf("fetch Beacon genesis: %w", err)
	}
	timestamp, err := parseDecimal("genesis time", response.Data.GenesisTime)
	if err != nil {
		return Genesis{}, err
	}
	validatorsRoot, err := parseRoot("genesis validators root", response.Data.GenesisValidatorsRoot)
	if err != nil {
		return Genesis{}, err
	}
	forkVersion, err := parseFixedHex("genesis fork version", response.Data.GenesisForkVersion, 4)
	if err != nil {
		return Genesis{}, err
	}
	var headerResponse struct {
		Data struct {
			Canonical bool `json:"canonical"`
			Header    struct {
				Message struct {
					Slot string `json:"slot"`
				} `json:"message"`
			} `json:"header"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, "/eth/v1/beacon/headers/genesis", &headerResponse); err != nil {
		return Genesis{}, fmt.Errorf("fetch Beacon genesis header: %w", err)
	}
	if !headerResponse.Data.Canonical {
		return Genesis{}, errors.New("Beacon genesis header is not canonical")
	}
	genesisSlot, err := parseDecimal("genesis slot", headerResponse.Data.Header.Message.Slot)
	if err != nil {
		return Genesis{}, err
	}
	return Genesis{Time: timestamp, Slot: genesisSlot, ValidatorsRoot: validatorsRoot, ForkVersion: forkVersion}, nil
}

func (c *Client) Head(ctx context.Context) (Head, error) {
	var response struct {
		ExecutionOptimistic bool `json:"execution_optimistic"`
		Data                struct {
			Root      string `json:"root"`
			Canonical bool   `json:"canonical"`
			Header    struct {
				Message struct {
					Slot       string `json:"slot"`
					ParentRoot string `json:"parent_root"`
					StateRoot  string `json:"state_root"`
				} `json:"message"`
			} `json:"header"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, "/eth/v1/beacon/headers/head", &response); err != nil {
		return Head{}, fmt.Errorf("fetch Beacon head: %w", err)
	}
	if !response.Data.Canonical {
		return Head{}, errors.New("Beacon head is not canonical")
	}

	slot, err := parseDecimal("head slot", response.Data.Header.Message.Slot)
	if err != nil {
		return Head{}, err
	}
	root, err := parseRoot("head root", response.Data.Root)
	if err != nil {
		return Head{}, err
	}
	parentRoot, err := parseRoot("head parent root", response.Data.Header.Message.ParentRoot)
	if err != nil {
		return Head{}, err
	}
	stateRoot, err := parseRoot("head state root", response.Data.Header.Message.StateRoot)
	if err != nil {
		return Head{}, err
	}

	return Head{
		Slot:                slot,
		Root:                root,
		ParentRoot:          parentRoot,
		StateRoot:           stateRoot,
		Canonical:           true,
		ExecutionOptimistic: response.ExecutionOptimistic,
	}, nil
}

func (c *Client) Finality(ctx context.Context, stateRoot string) (Finality, error) {
	validatedRoot, err := parseRoot("finality state root", stateRoot)
	if err != nil {
		return Finality{}, err
	}
	var response struct {
		ExecutionOptimistic bool `json:"execution_optimistic"`
		Data                struct {
			Finalized struct {
				Epoch string `json:"epoch"`
				Root  string `json:"root"`
			} `json:"finalized"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, "/eth/v1/beacon/states/"+validatedRoot+"/finality_checkpoints", &response); err != nil {
		return Finality{}, fmt.Errorf("fetch finality checkpoints: %w", err)
	}

	epoch, err := parseDecimal("finalized epoch", response.Data.Finalized.Epoch)
	if err != nil {
		return Finality{}, err
	}
	root, err := parseRoot("finalized root", response.Data.Finalized.Root)
	if err != nil {
		return Finality{}, err
	}

	return Finality{
		Epoch:               epoch,
		Root:                root,
		ExecutionOptimistic: response.ExecutionOptimistic,
	}, nil
}

func (c *Client) Validators(ctx context.Context) ([]Validator, error) {
	var response struct {
		Data []struct {
			Index     string `json:"index"`
			Status    string `json:"status"`
			Validator struct {
				EffectiveBalance string `json:"effective_balance"`
			} `json:"validator"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, "/eth/v1/beacon/states/head/validators", &response); err != nil {
		return nil, fmt.Errorf("fetch state validators: %w", err)
	}
	if len(response.Data) == 0 {
		return nil, errors.New("state validators response is empty")
	}
	validators := make([]Validator, 0, len(response.Data))
	seen := make(map[uint64]struct{}, len(response.Data))
	for _, item := range response.Data {
		index, err := parseDecimal("validator index", item.Index)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[index]; exists {
			return nil, fmt.Errorf("validator index %d is duplicated", index)
		}
		effectiveBalance, err := parseDecimal("validator effective balance", item.Validator.EffectiveBalance)
		if err != nil {
			return nil, err
		}
		if effectiveBalance == 0 {
			return nil, fmt.Errorf("validator %d has zero effective balance", index)
		}
		if !strings.HasPrefix(item.Status, "active_") {
			return nil, fmt.Errorf("validator %d is not active: %q", index, item.Status)
		}
		seen[index] = struct{}{}
		validators = append(validators, Validator{
			Index:            index,
			EffectiveBalance: effectiveBalance,
			Status:           item.Status,
		})
	}
	return validators, nil
}

func (c *Client) getJSON(ctx context.Context, endpointPath string, destination any) error {
	requestURL := *c.base
	requestURL.Path = strings.TrimRight(requestURL.Path, "/") + endpointPath

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}

	if response.StatusCode != http.StatusOK {
		_, drainErr := io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		closeErr := response.Body.Close()
		statusErr := fmt.Errorf("unexpected HTTP status %s", response.Status)
		if drainErr != nil {
			statusErr = errors.Join(statusErr, fmt.Errorf("discard error response: %w", drainErr))
		}
		if closeErr != nil {
			statusErr = errors.Join(statusErr, fmt.Errorf("close error response: %w", closeErr))
		}
		return statusErr
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	closeErr := response.Body.Close()
	if err != nil {
		return errors.Join(fmt.Errorf("read response: %w", err), closeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close response: %w", closeErr)
	}
	if len(body) > maxResponseBytes {
		return fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("decode response JSON: %w", err)
	}
	return nil
}

func requiredDecimalField(data map[string]json.RawMessage, name string) (uint64, error) {
	raw, exists := data[name]
	if !exists {
		return 0, fmt.Errorf("required field %s is missing", name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("field %s is not a decimal string: %w", name, err)
	}
	return parseDecimal(name, value)
}

func parseDecimal(name, value string) (uint64, error) {
	if value == "" {
		return 0, fmt.Errorf("%s is empty", name)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, fmt.Errorf("%s %q is not an unsigned decimal", name, value)
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s %q: %w", name, value, err)
	}
	return parsed, nil
}

func parseRoot(name, value string) (string, error) {
	return parseFixedHex(name, value, 32)
}

func parseFixedHex(name, value string, byteLength int) (string, error) {
	if len(value) != 2+byteLength*2 || !strings.HasPrefix(value, "0x") {
		return "", fmt.Errorf("%s must be a %d-byte 0x-prefixed hex value", name, byteLength)
	}
	if _, err := hex.DecodeString(value[2:]); err != nil {
		return "", fmt.Errorf("parse %s: %w", name, err)
	}
	return strings.ToLower(value), nil
}

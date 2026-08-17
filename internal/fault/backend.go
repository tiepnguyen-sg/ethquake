// Package fault defines the replaceable fault-injection boundary.
package fault

import (
	"context"
	"errors"
	"time"
)

type PartitionRequest struct {
	RunID           string
	Namespace       string
	NamespacePrefix string
	GroupA          []string
	GroupB          []string
	TTL             time.Duration
}

type Status struct {
	Exists       bool
	AllInjected  bool
	AllRecovered bool
}

type Backend interface {
	Apply(context.Context, PartitionRequest) error
	Revert(context.Context, PartitionRequest) error
	Status(context.Context, PartitionRequest) (Status, error)
}

var ErrOwnership = errors.New("fault resource ownership check failed")

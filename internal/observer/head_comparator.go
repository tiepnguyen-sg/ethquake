package observer

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
)

type HeadComparison struct {
	Timestamp time.Time         `json:"timestamp"`
	Slot      uint64            `json:"slot"`
	Roots     map[string]string `json:"roots"`
	Agreement bool              `json:"agreement"`
}

type headSlotState struct {
	roots         map[string]string
	lastSignature string
}

type HeadComparator struct {
	mu           sync.Mutex
	targets      []string
	historyLimit int
	slots        map[uint64]*headSlotState
}

func NewHeadComparator(targets []string, historyLimit int) (*HeadComparator, error) {
	if len(targets) < 2 {
		return nil, errors.New("at least two targets are required for head comparison")
	}
	if historyLimit < 2 {
		return nil, errors.New("head history limit must be at least 2")
	}
	targetCopy := append([]string(nil), targets...)
	sort.Strings(targetCopy)
	for index, target := range targetCopy {
		if target == "" {
			return nil, errors.New("head comparison target must not be empty")
		}
		if index > 0 && target == targetCopy[index-1] {
			return nil, errors.New("head comparison targets must be unique")
		}
	}
	return &HeadComparator{
		targets:      targetCopy,
		historyLimit: historyLimit,
		slots:        make(map[uint64]*headSlotState),
	}, nil
}

func (c *HeadComparator) Observe(timestamp time.Time, target string, head beacon.Head) *HeadComparison {
	c.mu.Lock()
	defer c.mu.Unlock()

	state, exists := c.slots[head.Slot]
	if !exists {
		state = &headSlotState{roots: make(map[string]string, len(c.targets))}
		c.slots[head.Slot] = state
	}
	state.roots[target] = head.Root
	if len(state.roots) != len(c.targets) {
		c.trim()
		return nil
	}

	var signatureBuilder strings.Builder
	roots := make(map[string]string, len(c.targets))
	agreement := true
	firstRoot := ""
	for _, targetName := range c.targets {
		root, present := state.roots[targetName]
		if !present {
			return nil
		}
		roots[targetName] = root
		signatureBuilder.WriteString(targetName)
		signatureBuilder.WriteByte('=')
		signatureBuilder.WriteString(root)
		signatureBuilder.WriteByte('\n')
		if firstRoot == "" {
			firstRoot = root
		} else if root != firstRoot {
			agreement = false
		}
	}
	signature := signatureBuilder.String()
	if signature == state.lastSignature {
		return nil
	}
	state.lastSignature = signature
	c.trim()
	return &HeadComparison{
		Timestamp: timestamp.UTC(),
		Slot:      head.Slot,
		Roots:     roots,
		Agreement: agreement,
	}
}

func (c *HeadComparator) trim() {
	if len(c.slots) <= c.historyLimit {
		return
	}
	slots := make([]uint64, 0, len(c.slots))
	for slot := range c.slots {
		slots = append(slots, slot)
	}
	sort.Slice(slots, func(left, right int) bool { return slots[left] < slots[right] })
	for _, slot := range slots[:len(slots)-c.historyLimit] {
		delete(c.slots, slot)
	}
}

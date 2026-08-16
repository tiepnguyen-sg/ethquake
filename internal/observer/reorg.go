package observer

import (
	"fmt"
	"math"
	"time"

	"github.com/tiepnguyen-sg/ethquake/internal/execution"
)

type ReorgObservation struct {
	Timestamp    time.Time      `json:"timestamp"`
	Target       string         `json:"target"`
	PreviousHead execution.Head `json:"previous_head"`
	NewHead      execution.Head `json:"new_head"`
	Depth        *uint64        `json:"depth,omitempty"`
}

type ReorgResult struct {
	Reorg        *ReorgObservation
	Continuity   bool
	GapOperation string
	GapError     string
}

type ReorgTracker struct {
	target       string
	historyLimit int
	canonical    []execution.Head
}

func NewReorgTracker(target string, historyLimit int) (*ReorgTracker, error) {
	if target == "" {
		return nil, fmt.Errorf("execution target name must not be empty")
	}
	if historyLimit < 2 {
		return nil, fmt.Errorf("reorg history limit must be at least 2")
	}
	return &ReorgTracker{target: target, historyLimit: historyLimit}, nil
}

func (t *ReorgTracker) Observe(head execution.Head) ReorgResult {
	if len(t.canonical) == 0 {
		t.canonical = append(t.canonical, head)
		return ReorgResult{Continuity: true}
	}

	tip := t.canonical[len(t.canonical)-1]
	if head.Hash == tip.Hash {
		return ReorgResult{Continuity: true}
	}
	if tip.Number != math.MaxUint64 && head.Number == tip.Number+1 && head.ParentHash == tip.Hash {
		t.appendCanonical(head)
		return ReorgResult{Continuity: true}
	}

	parentIndex := t.canonicalIndex(head.ParentHash)
	if parentIndex >= 0 {
		parent := t.canonical[parentIndex]
		if parent.Number > tip.Number || parent.Number == math.MaxUint64 || head.Number != parent.Number+1 {
			t.reset(head)
			return ReorgResult{
				Continuity:   false,
				GapOperation: "execution_continuity",
				GapError:     "new head number is not the successor of its known parent",
			}
		}
		depth := tip.Number - parent.Number
		t.canonical = append(t.canonical[:parentIndex+1], head)
		t.trim()
		return ReorgResult{
			Continuity: true,
			Reorg: &ReorgObservation{
				Target:       t.target,
				PreviousHead: tip,
				NewHead:      head,
				Depth:        &depth,
			},
		}
	}

	if tip.Number != math.MaxUint64 && head.Number > tip.Number+1 {
		t.reset(head)
		return ReorgResult{
			Continuity:   false,
			GapOperation: "execution_continuity",
			GapError:     fmt.Sprintf("head sequence skipped from block %d to %d", tip.Number, head.Number),
		}
	}

	t.reset(head)
	return ReorgResult{
		Continuity: false,
		Reorg: &ReorgObservation{
			Target:       t.target,
			PreviousHead: tip,
			NewHead:      head,
			Depth:        nil,
		},
	}
}

func (t *ReorgTracker) canonicalIndex(hash string) int {
	for index := len(t.canonical) - 1; index >= 0; index-- {
		if t.canonical[index].Hash == hash {
			return index
		}
	}
	return -1
}

func (t *ReorgTracker) appendCanonical(head execution.Head) {
	t.canonical = append(t.canonical, head)
	t.trim()
}

func (t *ReorgTracker) trim() {
	if len(t.canonical) <= t.historyLimit {
		return
	}
	copy(t.canonical, t.canonical[len(t.canonical)-t.historyLimit:])
	t.canonical = t.canonical[:t.historyLimit]
}

func (t *ReorgTracker) reset(head execution.Head) {
	t.canonical = t.canonical[:0]
	t.canonical = append(t.canonical, head)
}

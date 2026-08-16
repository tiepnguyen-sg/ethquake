package observer

import (
	"testing"
	"time"

	"github.com/tiepnguyen-sg/ethquake/internal/beacon"
)

func TestHeadComparatorEmitsExactSameSlotAgreement(t *testing.T) {
	comparator, err := NewHeadComparator([]string{"teku", "lighthouse"}, 16)
	if err != nil {
		t.Fatalf("NewHeadComparator() error = %v", err)
	}
	timestamp := time.Unix(100, 0)
	if comparison := comparator.Observe(timestamp, "lighthouse", beacon.Head{Slot: 10, Root: root("1")}); comparison != nil {
		t.Fatalf("first target comparison = %+v", comparison)
	}
	comparison := comparator.Observe(timestamp, "teku", beacon.Head{Slot: 10, Root: root("1")})
	if comparison == nil || !comparison.Agreement || comparison.Slot != 10 {
		t.Fatalf("comparison = %+v", comparison)
	}
	if comparison.Roots["lighthouse"] != root("1") || comparison.Roots["teku"] != root("1") {
		t.Fatalf("comparison roots = %+v", comparison.Roots)
	}
	if duplicate := comparator.Observe(timestamp, "teku", beacon.Head{Slot: 10, Root: root("1")}); duplicate != nil {
		t.Fatalf("duplicate comparison = %+v", duplicate)
	}
}

func TestHeadComparatorEmitsChangedRootAsDivergence(t *testing.T) {
	comparator, err := NewHeadComparator([]string{"lighthouse", "teku"}, 16)
	if err != nil {
		t.Fatalf("NewHeadComparator() error = %v", err)
	}
	comparator.Observe(time.Time{}, "lighthouse", beacon.Head{Slot: 10, Root: root("1")})
	comparator.Observe(time.Time{}, "teku", beacon.Head{Slot: 10, Root: root("1")})
	comparison := comparator.Observe(time.Time{}, "teku", beacon.Head{Slot: 10, Root: root("2")})
	if comparison == nil || comparison.Agreement {
		t.Fatalf("changed-root comparison = %+v", comparison)
	}
}

func TestHeadComparatorDoesNotCompareDifferentSlots(t *testing.T) {
	comparator, err := NewHeadComparator([]string{"lighthouse", "teku"}, 16)
	if err != nil {
		t.Fatalf("NewHeadComparator() error = %v", err)
	}
	comparator.Observe(time.Time{}, "lighthouse", beacon.Head{Slot: 10, Root: root("1")})
	if comparison := comparator.Observe(time.Time{}, "teku", beacon.Head{Slot: 11, Root: root("2")}); comparison != nil {
		t.Fatalf("different-slot comparison = %+v", comparison)
	}
	comparison := comparator.Observe(time.Time{}, "lighthouse", beacon.Head{Slot: 11, Root: root("2")})
	if comparison == nil || !comparison.Agreement || comparison.Slot != 11 {
		t.Fatalf("eventual same-slot comparison = %+v", comparison)
	}
}

func TestHeadComparatorRejectsInvalidConfiguration(t *testing.T) {
	if _, err := NewHeadComparator([]string{"one"}, 16); err == nil {
		t.Fatal("single-target NewHeadComparator() error = nil")
	}
	if _, err := NewHeadComparator([]string{"one", "two"}, 1); err == nil {
		t.Fatal("short-history NewHeadComparator() error = nil")
	}
	if _, err := NewHeadComparator([]string{"same", "same"}, 16); err == nil {
		t.Fatal("duplicate-target NewHeadComparator() error = nil")
	}
}

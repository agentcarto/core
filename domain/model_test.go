package domain

import (
	"testing"
	"time"
)

func TestActivePath(t *testing.T) {
	c := NewConversation([]ConvNode{{ID: "a"}, {ID: "b", Parent: "a"}, {ID: "side", Parent: "a"}, {ID: "c", Parent: "b"}})
	got := c.ActivePath()
	want := []string{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v", got)
		}
	}
}

// A corrupt transcript whose parent links form a cycle (X<->Y) upstream of a
// leaf must not make ActivePath loop forever. Before the fix the walk appended
// to its result without bound, ramping RSS until the process was OOM-killed.
func TestActivePathCycleTerminates(t *testing.T) {
	base := time.Unix(1000, 0)
	c := NewConversation([]ConvNode{
		{ID: "X", Parent: "Y", Timestamp: base},
		{ID: "Y", Parent: "X", Timestamp: base},
		{ID: "L", Parent: "X", Timestamp: base.Add(time.Second)},
	})
	done := make(chan []string, 1)
	go func() { done <- c.ActivePath() }()
	select {
	case got := <-done:
		if len(got) > len(c.Nodes) {
			t.Fatalf("ActivePath visited a node twice: %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ActivePath did not terminate on a cyclic parent chain")
	}
}

func TestChildrenSortedByTimestamp(t *testing.T) {
	c := NewConversation([]ConvNode{
		{ID: "a", Timestamp: time.Unix(1, 0)},
		{ID: "late", Parent: "a", Timestamp: time.Unix(9, 0)},
		{ID: "early", Parent: "a", Timestamp: time.Unix(2, 0)},
	})
	got := c.Children["a"]
	if len(got) != 2 || got[0] != "early" || got[1] != "late" {
		t.Fatalf("children=%v", got)
	}
}

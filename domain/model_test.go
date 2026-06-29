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

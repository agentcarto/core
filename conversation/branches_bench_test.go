package conversation

import (
	"fmt"
	"github.com/agentcarto/core/domain"
	"testing"
)

func BenchmarkTurnsOfPath(b *testing.B) {
	nodes := make([]domain.ConvNode, 0, 10000)
	parent := ""
	for i := 0; i < 10000; i++ {
		id := fmt.Sprint(i)
		kind := domain.EventAssistant
		if i%10 == 0 {
			kind = domain.EventUser
		}
		nodes = append(nodes, domain.ConvNode{ID: id, Parent: parent, Events: []domain.Event{{Kind: kind, Text: "message"}}})
		parent = id
	}
	c := domain.NewConversation(nodes)
	path := c.ActivePath()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = TurnsOfPath(c, path)
	}
}

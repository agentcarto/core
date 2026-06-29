package plugin

import (
	"context"
	"fmt"
	"github.com/agentcarto/core/domain"
	"gopkg.in/yaml.v3"
)

// ScanInput hands the whole previous scan result to the plugin so it can skip
// re-parsing (incremental scan). It passes the data by value across the process
// boundary instead of using per-path host callbacks. The plugin computes
// fingerprints and makes the reuse/skip decisions itself via core/scan's Cache
// (the fingerprint is a hash of path:size:mtime, so both processes reproduce the
// same value).
type ScanInput struct {
	Warm []domain.Session  // Sessions from the previous snapshot (keyed by path = SourceRef.Source)
	Dead map[string]string // previous negative cache (path -> fingerprint)
}

// ScanOutput is the scan result. Dead is this run's negative cache (passed back
// as ScanInput.Dead on the next scan).
type ScanOutput struct {
	Sessions []domain.Session
	Dead     map[string]string
}

type Scanner interface {
	Scan(context.Context, ScanInput) (ScanOutput, error)
}
type ConversationLoader interface {
	LoadConversation(context.Context, domain.SessionRef) (*domain.Conversation, error)
}
type Resumer interface {
	ResumeCommand(domain.Session) (domain.Command, error)
}
type ExecutableProvider interface{ Executable() string }
type Rewinder interface {
	PlanFork(context.Context, domain.Session, domain.ForkTarget) (domain.MutationPlan, domain.Command, error)
}
type Relocator interface {
	PlanRelocate(context.Context, string, string, []domain.Session) (domain.MutationPlan, error)
}
type ActiveMatcher interface {
	DetectActive(context.Context, []domain.Session, []domain.Process) ([]domain.Session, error)
}
type Descriptor struct {
	Type, DisplayName, ParserVersion string
	// Executable is the executable name of a plugin that implements
	// ExecutableProvider (empty if it does not). It is resolved once during Init
	// at startup and stored here, so the host need not query it across the
	// subprocess boundary on every call.
	Executable   string
	Capabilities domain.Capabilities
}
type Instance struct {
	ID, Color  string
	Descriptor Descriptor
	Impl       any
	LastError  error
}
type Factory interface {
	Descriptor() Descriptor
	New(id string, options *yaml.Node) (any, error)
}
type Registry struct{ factories map[string]Factory }

func NewRegistry() *Registry { return &Registry{factories: map[string]Factory{}} }
func (r *Registry) Register(f Factory) error {
	d := f.Descriptor()
	if d.Type == "" {
		return fmt.Errorf("plugin type is empty")
	}
	if _, ok := r.factories[d.Type]; ok {
		return fmt.Errorf("plugin %q already registered", d.Type)
	}
	r.factories[d.Type] = f
	return nil
}
func (r *Registry) Types() []string {
	out := make([]string, 0, len(r.factories))
	for k := range r.factories {
		out = append(out, k)
	}
	return out
}
func (r *Registry) Build(id, typ, color string, options *yaml.Node) (Instance, error) {
	f, ok := r.factories[typ]
	if !ok {
		return Instance{}, fmt.Errorf("unknown plugin type %q", typ)
	}
	v, err := f.New(id, options)
	if err != nil {
		return Instance{}, err
	}
	d := f.Descriptor()
	c := d.Capabilities
	// Every advertised capability must be backed by the matching interface on the
	// built implementation, otherwise the host would call a method it does not
	// have.
	checks := []struct {
		on         bool
		name       string
		implements func(any) bool
	}{
		{c.Scan, "scan", func(x any) bool { _, ok := x.(Scanner); return ok }},
		{c.Conversation, "conversation", func(x any) bool { _, ok := x.(ConversationLoader); return ok }},
		{c.Active, "active", func(x any) bool { _, ok := x.(ActiveMatcher); return ok }},
		{c.Resume, "resume", func(x any) bool { _, ok := x.(Resumer); return ok }},
		{c.Rewind, "rewind", func(x any) bool { _, ok := x.(Rewinder); return ok }},
		{c.Relocate, "relocate", func(x any) bool { _, ok := x.(Relocator); return ok }},
	}
	for _, chk := range checks {
		if chk.on && !chk.implements(v) {
			return Instance{}, fmt.Errorf("plugin %s advertises %s but does not implement it", id, chk.name)
		}
	}
	return Instance{ID: id, Color: color, Descriptor: d, Impl: v}, nil
}

package plugin

// This file is the host<->plugin inter-process bridge built on
// hashicorp/go-plugin (net/rpc + gob mode). The domain types are all plain value
// types, so they transfer over gob as-is. Each plugin binary serves exactly one
// plugin type under the fixed key PluginSetName.

import (
	"context"
	"errors"
	"fmt"
	"net/rpc"
	"os"
	"os/exec"

	"github.com/agentcarto/core/domain"
	hclog "github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"gopkg.in/yaml.v3"
)

// Handshake is the startup handshake between host and plugin. A mismatched
// MagicCookie rejects accidental launches (i.e. running the binary normally).
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "AGENTCARTO_PLUGIN",
	MagicCookieValue: "agentcarto-v1",
}

// PluginSetName is the fixed key used in go-plugin's Plugins map (one plugin
// type per binary).
const PluginSetName = "agent"

// --- wire types (all fields exported so gob can encode them) ---

type InitArgs struct {
	ID      string
	Options []byte // yaml.Marshal'd options; empty means no options
}
type InitReply struct{ Desc Descriptor }

type ScanArgs struct{ In ScanInput }
type ScanReply struct{ Out ScanOutput }

type LoadConvArgs struct{ Ref domain.SessionRef }
type LoadConvReply struct {
	Conv domain.Conversation
	Has  bool
}

type ResumeArgs struct{ S domain.Session }
type ResumeReply struct{ Cmd domain.Command }

type PlanForkArgs struct {
	S domain.Session
	T domain.ForkTarget
}
type PlanForkReply struct {
	Plan domain.MutationPlan
	Cmd  domain.Command
}

type PlanRelocateArgs struct {
	Old, New string
	Sessions []domain.Session
}
type PlanRelocateReply struct{ Plan domain.MutationPlan }

type DetectActiveArgs struct {
	Sessions []domain.Session
	Procs    []domain.Process
}
type DetectActiveReply struct{ Out []domain.Session }

// --- server (plugin-process side) ---

// rpcServer is the object that go-plugin registers with net/rpc under the name
// "Plugin". Init builds the implementation from the Factory, and subsequent
// calls are delegated to that implementation. Methods for unimplemented
// capabilities return an error (normally they are never called, since the host
// gates them on Descriptor.Capabilities).
type rpcServer struct {
	factory Factory
	impl    any
}

func (s *rpcServer) Init(a InitArgs, r *InitReply) error {
	var node *yaml.Node
	if len(a.Options) > 0 {
		var doc yaml.Node
		if err := yaml.Unmarshal(a.Options, &doc); err != nil {
			return err
		}
		if len(doc.Content) > 0 {
			node = doc.Content[0]
		}
	}
	impl, err := s.factory.New(a.ID, node)
	if err != nil {
		return err
	}
	s.impl = impl
	d := s.factory.Descriptor()
	if ep, ok := impl.(ExecutableProvider); ok {
		d.Executable = ep.Executable()
	}
	r.Desc = d
	return nil
}

func (s *rpcServer) Scan(a ScanArgs, r *ScanReply) error {
	sc, ok := s.impl.(Scanner)
	if !ok {
		return errors.New("plugin does not implement Scanner")
	}
	out, err := sc.Scan(context.Background(), a.In)
	r.Out = out
	return err
}

func (s *rpcServer) LoadConversation(a LoadConvArgs, r *LoadConvReply) error {
	cl, ok := s.impl.(ConversationLoader)
	if !ok {
		return errors.New("plugin does not implement ConversationLoader")
	}
	conv, err := cl.LoadConversation(context.Background(), a.Ref)
	if err != nil {
		return err
	}
	if conv != nil {
		r.Conv = *conv
		r.Has = true
	}
	return nil
}

func (s *rpcServer) ResumeCommand(a ResumeArgs, r *ResumeReply) error {
	rs, ok := s.impl.(Resumer)
	if !ok {
		return errors.New("plugin does not implement Resumer")
	}
	cmd, err := rs.ResumeCommand(a.S)
	r.Cmd = cmd
	return err
}

func (s *rpcServer) PlanFork(a PlanForkArgs, r *PlanForkReply) error {
	rw, ok := s.impl.(Rewinder)
	if !ok {
		return errors.New("plugin does not implement Rewinder")
	}
	plan, cmd, err := rw.PlanFork(context.Background(), a.S, a.T)
	r.Plan, r.Cmd = plan, cmd
	return err
}

func (s *rpcServer) PlanRelocate(a PlanRelocateArgs, r *PlanRelocateReply) error {
	rl, ok := s.impl.(Relocator)
	if !ok {
		return errors.New("plugin does not implement Relocator")
	}
	plan, err := rl.PlanRelocate(context.Background(), a.Old, a.New, a.Sessions)
	r.Plan = plan
	return err
}

func (s *rpcServer) DetectActive(a DetectActiveArgs, r *DetectActiveReply) error {
	am, ok := s.impl.(ActiveMatcher)
	if !ok {
		return errors.New("plugin does not implement ActiveMatcher")
	}
	out, err := am.DetectActive(context.Background(), a.Sessions, a.Procs)
	r.Out = out
	return err
}

// --- client (host side) ---

// Client is the RPC client the host uses. It implements all of the plugin's
// capability interfaces, so the host's type assertions (Impl.(plugin.Scanner),
// etc.) keep working even after the plugin runs in a subprocess. It does not
// implement ExecutableProvider, since that was folded into Descriptor.Executable.
type Client struct{ rpc *rpc.Client }

// Init passes id and options to the plugin so it can build its implementation,
// and returns the resulting Descriptor.
func (c *Client) Init(id string, options *yaml.Node) (Descriptor, error) {
	var b []byte
	if options != nil && options.Kind != 0 {
		var err error
		if b, err = yaml.Marshal(options); err != nil {
			return Descriptor{}, err
		}
	}
	var r InitReply
	err := c.rpc.Call("Plugin.Init", InitArgs{ID: id, Options: b}, &r)
	return r.Desc, err
}

func (c *Client) Scan(_ context.Context, in ScanInput) (ScanOutput, error) {
	var r ScanReply
	err := c.rpc.Call("Plugin.Scan", ScanArgs{In: in}, &r)
	return r.Out, err
}

func (c *Client) LoadConversation(_ context.Context, ref domain.SessionRef) (*domain.Conversation, error) {
	var r LoadConvReply
	if err := c.rpc.Call("Plugin.LoadConversation", LoadConvArgs{Ref: ref}, &r); err != nil {
		return nil, err
	}
	if !r.Has {
		return nil, nil
	}
	conv := r.Conv
	return &conv, nil
}

func (c *Client) ResumeCommand(s domain.Session) (domain.Command, error) {
	var r ResumeReply
	err := c.rpc.Call("Plugin.ResumeCommand", ResumeArgs{S: s}, &r)
	return r.Cmd, err
}

func (c *Client) PlanFork(_ context.Context, s domain.Session, t domain.ForkTarget) (domain.MutationPlan, domain.Command, error) {
	var r PlanForkReply
	err := c.rpc.Call("Plugin.PlanFork", PlanForkArgs{S: s, T: t}, &r)
	return r.Plan, r.Cmd, err
}

func (c *Client) PlanRelocate(_ context.Context, old, nw string, sessions []domain.Session) (domain.MutationPlan, error) {
	var r PlanRelocateReply
	err := c.rpc.Call("Plugin.PlanRelocate", PlanRelocateArgs{Old: old, New: nw, Sessions: sessions}, &r)
	return r.Plan, err
}

func (c *Client) DetectActive(_ context.Context, sessions []domain.Session, procs []domain.Process) ([]domain.Session, error) {
	var r DetectActiveReply
	err := c.rpc.Call("Plugin.DetectActive", DetectActiveArgs{Sessions: sessions, Procs: procs}, &r)
	return r.Out, err
}

// --- go-plugin wiring ---

// AgentPlugin is the go-plugin plugin.Plugin (net/rpc) implementation. On the
// plugin side, Factory is set and passed to Serve; on the host side it is left
// nil and a Client is received instead.
type AgentPlugin struct{ Factory Factory }

func (p *AgentPlugin) Server(*goplugin.MuxBroker) (interface{}, error) {
	return &rpcServer{factory: p.Factory}, nil
}
func (p *AgentPlugin) Client(_ *goplugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &Client{rpc: c}, nil
}

// Serve is the entry point of a plugin process. Each plugin-*'s cmd/main.go calls
// it, passing in its factory.
func Serve(factory Factory) {
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         map[string]goplugin.Plugin{PluginSetName: &AgentPlugin{Factory: factory}},
		Logger: hclog.New(&hclog.LoggerOptions{
			Level:      hclog.Error,
			Output:     os.Stderr,
			JSONFormat: true,
		}),
	})
}

// Launched is a handle to a plugin process started by the host.
type Launched struct {
	client *goplugin.Client
	API    *Client
}

// Kill terminates the plugin process.
func (l *Launched) Kill() {
	if l != nil && l.client != nil {
		l.client.Kill()
	}
}

// Launch starts the plugin binary, dispenses it, and returns its Client. On
// failure it cleans up the process.
func Launch(binPath string) (*Launched, error) {
	c := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig:  Handshake,
		Plugins:          map[string]goplugin.Plugin{PluginSetName: &AgentPlugin{}},
		Cmd:              exec.Command(binPath),
		Managed:          true,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolNetRPC},
		Logger: hclog.New(&hclog.LoggerOptions{
			Level:  hclog.Error,
			Output: os.Stderr,
		}),
	})
	rpcClient, err := c.Client()
	if err != nil {
		c.Kill()
		return nil, err
	}
	raw, err := rpcClient.Dispense(PluginSetName)
	if err != nil {
		c.Kill()
		return nil, err
	}
	api, ok := raw.(*Client)
	if !ok {
		c.Kill()
		return nil, fmt.Errorf("unexpected plugin client type %T", raw)
	}
	return &Launched{client: c, API: api}, nil
}

// CleanupClients terminates all plugin processes started by go-plugin. Call it
// when the host process is shutting down.
func CleanupClients() { goplugin.CleanupClients() }

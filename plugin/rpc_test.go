package plugin

// In-process round-trip tests for the host<->plugin net/rpc bridge: the same
// rpcServer/Client pair used across the subprocess boundary, wired over a
// net.Pipe. This covers the yaml option encoding of Init, gob transfer of
// domain values, and ctx cancellation on the client side.

import (
	"context"
	"errors"
	"net"
	"net/rpc"
	"testing"
	"time"

	"github.com/agentcarto/core/domain"
	goplugin "github.com/hashicorp/go-plugin"
	"gopkg.in/yaml.v3"
)

type fakeOptions struct {
	Dir string `yaml:"dir"`
}

type fakeImpl struct {
	opts  fakeOptions
	block chan struct{} // when non-nil, Scan blocks until closed
}

func (f *fakeImpl) Scan(_ context.Context, in ScanInput) (ScanOutput, error) {
	if f.block != nil {
		<-f.block
	}
	return ScanOutput{Sessions: []domain.Session{{PluginID: "p", SessionID: "s1", CWD: f.opts.Dir, Unresumable: in.IncludeInternal}}, Dead: in.Dead}, nil
}

type fakeFactory struct{ impl *fakeImpl }

func (f *fakeFactory) Descriptor() Descriptor {
	return Descriptor{Type: "fake", DisplayName: "Fake", ParserVersion: "v1", Capabilities: domain.Capabilities{Scan: true}}
}
func (f *fakeFactory) New(id string, options *yaml.Node) (any, error) {
	if options != nil {
		if e := options.Decode(&f.impl.opts); e != nil {
			return nil, e
		}
	}
	return f.impl, nil
}

// bridge wires a Client to an rpcServer over an in-memory pipe.
func bridge(t *testing.T, impl *fakeImpl) *Client {
	t.Helper()
	srvConn, cliConn := net.Pipe()
	srv := rpc.NewServer()
	if e := srv.RegisterName("Plugin", &rpcServer{factory: &fakeFactory{impl: impl}}); e != nil {
		t.Fatal(e)
	}
	go srv.ServeConn(srvConn)
	c := &Client{rpc: rpc.NewClient(cliConn)}
	t.Cleanup(func() { _ = cliConn.Close(); _ = srvConn.Close() })
	return c
}

func TestRPCInitAndScanRoundTrip(t *testing.T) {
	impl := &fakeImpl{}
	c := bridge(t, impl)
	var opts yaml.Node
	if e := yaml.Unmarshal([]byte("dir: /tmp/x"), &opts); e != nil {
		t.Fatal(e)
	}
	desc, e := c.Init(context.Background(), "fake", opts.Content[0])
	if e != nil {
		t.Fatal(e)
	}
	if desc.Type != "fake" || desc.ParserVersion != "v1" || !desc.Capabilities.Scan {
		t.Fatalf("descriptor did not survive the round trip: %+v", desc)
	}
	if impl.opts.Dir != "/tmp/x" {
		t.Fatalf("yaml options did not survive the round trip: %+v", impl.opts)
	}
	out, e := c.Scan(context.Background(), ScanInput{Dead: map[string]string{"a": "fp"}, IncludeInternal: true})
	if e != nil {
		t.Fatal(e)
	}
	if len(out.Sessions) != 1 || out.Sessions[0].CWD != "/tmp/x" || out.Dead["a"] != "fp" || !out.Sessions[0].Unresumable {
		t.Fatalf("scan output did not survive the round trip: %+v", out)
	}
}

func TestRPCScanHonorsContextCancellation(t *testing.T) {
	impl := &fakeImpl{block: make(chan struct{})}
	defer close(impl.block)
	c := bridge(t, impl)
	if _, e := c.Init(context.Background(), "fake", nil); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, e := c.Scan(ctx, ScanInput{})
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", e)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("client did not unblock on ctx cancellation")
	}
}

func TestRPCUnimplementedCapabilityError(t *testing.T) {
	c := bridge(t, &fakeImpl{})
	if _, e := c.Init(context.Background(), "fake", nil); e != nil {
		t.Fatal(e)
	}
	if _, e := c.LoadConversation(context.Background(), domain.SessionRef{}); e == nil {
		t.Fatal("want error for unimplemented ConversationLoader")
	}
}

// A plugin told to quit must outlast the host's side of the connection: the
// host closes its streams after asking, and a plugin that is already gone turns
// each close into a failed write on the host's stderr. So the plugin has to be
// able to tell that the host hung up, and not think so while it is connected.
func TestPluginSeesTheHostHangUp(t *testing.T) {
	h := newHostHangup()
	p := &AgentPlugin{Factory: &fakeFactory{impl: &fakeImpl{}}, hangup: h}
	client, _ := goplugin.TestPluginRPCConn(t, map[string]goplugin.Plugin{PluginSetName: p}, nil)
	raw, err := client.Dispense(PluginSetName)
	if err != nil {
		t.Fatal(err)
	}
	// The bridge still works beside the extra stream.
	if _, err := raw.(*Client).Init(context.Background(), "p", nil); err != nil {
		t.Fatalf("Init over the bridge: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for !h.watching.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the plugin never saw the host open the stream")
		}
		time.Sleep(time.Millisecond)
	}
	if h.wait(50 * time.Millisecond) {
		t.Fatal("the plugin took a connected host for one that hung up")
	}

	_ = client.Close()
	if !h.wait(5 * time.Second) {
		t.Fatal("the plugin did not see the host hang up")
	}
}

// A host built before the stream existed never opens it. Waiting for a hang-up
// that cannot be seen would hold every exit of the plugin for the full limit.
func TestPluginDoesNotWaitForAHostThatNeverOpenedTheStream(t *testing.T) {
	h := newHostHangup()
	start := time.Now()
	if h.wait(time.Minute) {
		t.Fatal("a hang-up was reported with no stream to see it on")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("waited %v for a stream that was never opened", time.Since(start))
	}
}

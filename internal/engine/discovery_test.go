package engine

import (
	"testing"

	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/stretchr/testify/assert"
	"github.com/trustos/pulumi-ui/internal/programs"
)

func TestLooksLikeIP(t *testing.T) {
	assert.True(t, looksLikeIP("130.61.119.93"))
	assert.True(t, looksLikeIP("10.0.0.1"))
	assert.True(t, looksLikeIP("192.168.1.1"))
	assert.True(t, looksLikeIP("255.255.255.255"))

	assert.False(t, looksLikeIP(""))
	assert.False(t, looksLikeIP("not-an-ip"))
	assert.False(t, looksLikeIP("10.0.0"))
	assert.False(t, looksLikeIP("10.0.0.1.2"))
	assert.False(t, looksLikeIP("ocid1.compartment.oc1..aaaaaa"))
	assert.False(t, looksLikeIP("10.0.0.1/24"))
	assert.False(t, looksLikeIP("10.0.0.a"))
	assert.False(t, looksLikeIP("1234.0.0.1"))
}

func TestLooksLikeIP_BoundaryValues(t *testing.T) {
	assert.True(t, looksLikeIP("0.0.0.0"))
	assert.True(t, looksLikeIP("1.1.1.1"))
	assert.False(t, looksLikeIP("...."))
	assert.False(t, looksLikeIP("a.b.c.d"))
	assert.False(t, looksLikeIP(".1.1.1"))
	assert.False(t, looksLikeIP("1.1.1."))
}

func TestDiscoverAgentAddress_NilConnStore(t *testing.T) {
	e := &Engine{connStore: nil}
	var events []SSEEvent
	send := func(ev SSEEvent) { events = append(events, ev) }

	e.discoverAgentAddress(nil, "test", &plainProgram{}, auto.Stack{}, send)
	assert.Empty(t, events, "plain program + nil connStore should be a no-op")
}

func TestDiscoverAgentAddress_NonAgentProgram(t *testing.T) {
	e := &Engine{}
	var events []SSEEvent
	send := func(ev SSEEvent) { events = append(events, ev) }

	e.discoverAgentAddress(nil, "test", &plainProgram{}, auto.Stack{}, send)
	assert.Empty(t, events, "non-agent programs should be skipped before outputs are read")
}

// plainProgram satisfies programs.Program but not ApplicationProvider or AgentAccessProvider.
type plainProgram struct{}

func (p *plainProgram) Name() string                              { return "plain" }
func (p *plainProgram) DisplayName() string                       { return "Plain" }
func (p *plainProgram) Description() string                       { return "test" }
func (p *plainProgram) ConfigFields() []programs.ConfigField      { return nil }
func (p *plainProgram) Run(cfg map[string]string) pulumi.RunFunc  { return nil }

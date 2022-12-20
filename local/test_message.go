package local

import "github.com/ava-labs/avalanchego/message"

var _ message.OutboundMessage = &TestMsg{}

type TestMsg struct {
	op               message.Op
	bytes            []byte
	bypassThrottling bool
}

func NewTestMsg(op message.Op, bytes []byte, bypassThrottling bool) *TestMsg {
	return &TestMsg{
		op:               op,
		bytes:            bytes,
		bypassThrottling: bypassThrottling,
	}
}

func (m *TestMsg) BypassThrottling() bool {
	return m.bypassThrottling
}

func (m *TestMsg) Op() message.Op {
	return m.op
}

func (m *TestMsg) Bytes() []byte {
	return m.bytes
}

func (*TestMsg) BytesSavedCompression() int {
	return 0
}

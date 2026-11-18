package broker

import (
	"context"
	"errors"

	"github.com/wangzw/agent-peer/internal/link"
	"github.com/wangzw/agent-peer/internal/topic"
)

// topicAssembler builds the topic registry over the link that the pairing and
// link assembler created and installs it as Deps.Topics. topic.New registers
// the registry's frame handlers and link subscription, so it must run before
// Run binds the listener.
type topicAssembler struct{}

var (
	_ topic.Transport = (*link.Link)(nil)
	_ Assembler       = topicAssembler{}
)

// Name is the assembler's name in startup errors.
func (topicAssembler) Name() string { return "topic" }

// Assemble constructs the registry. It returns no component: the registry
// starts working as soon as link starts, and Run owns link's lifecycle.
func (topicAssembler) Assemble(_ context.Context, _ ConfigSource, deps *Deps, asm *Assembly) ([]Component, error) {
	if asm.Link == nil {
		return nil, errors.New("topic needs the link built by the pairing and link assembler")
	}
	r := topic.New(asm.Link, topic.Options{Logger: deps.Logger})
	deps.Topics = NewTopicTable(r)
	return nil, nil
}

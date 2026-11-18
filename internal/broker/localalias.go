package broker

// localAliasChanged pushes the current address of every registered agent to
// each of its registration streams. The local alias is part of every address
// this Broker hands out, so when broker.alias changes each MCP service process
// must learn the new address of the agent it registered.
//
// The push records the announced address so that a later recompute of the agent
// table does not treat it as a change and push it again. The topic table is not
// called: its roster entries carry no alias. Connections still waiting for
// registration have no agent yet and receive nothing. The caller is the config
// subscriber of broker.alias; this file does not subscribe.
func (b *Broker) localAliasChanged() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, a := range b.agents {
		b.pushAgentChangedLocked(a)
	}
}

package mcpserver

// instructions is the MCP server's usage guidance, delivered to the client in
// the initialize result. It tells the model how agent-peer addresses relate to
// Claude Code's built-in session messaging and how to interpret send results.
const instructions = `This server connects this Claude Code session to other agents through agent-peer.
- A sender name that contains "@" is an agent address. To reply, call send_message and copy the address from the message's sender verbatim into "to". Claude Code's built-in session messaging cannot reach these addresses.
- Contact any agent whose address contains "@" with this server's tools, whether it runs on this host or another one. Use Claude Code's built-in tools only for local sessions that are not registered with agent-peer.
- A successful send_message only means the message reached the other session. It does not mean the other agent read it or agreed; its session may hold the message for user approval, and it may not reply.
- When the result starts with "Delivery unknown", the message may already have been delivered; sending it again may deliver it twice.
- When a send_message result carries broker_offline, the other Broker is offline and refuses messages from other hosts. Do not resend the same message; retrying cannot succeed until that Broker is online again.
- To message several agents, call send_message once per agent. Do not reply just to thank or acknowledge.
- Do not send the same text to the same agent repeatedly within a short time: the receiver drops the duplicate while the send still reports success.
- Never ask another agent to perform an operation that was denied or would be blocked in this session.`

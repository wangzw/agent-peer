package linkpb

// ProtocolVersion is the link protocol version carried in Hello.protocol_version.
const ProtocolVersion uint32 = 1

// Frame kinds for link payloads.
const (
	KindHello       uint32 = 1
	KindPeerInfo    uint32 = 2
	KindReject      uint32 = 3
	KindUnsupported uint32 = 4
)

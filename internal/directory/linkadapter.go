package directory

import (
	"context"

	"github.com/wangzw/peers/internal/link"
)

// linkAPI is the part of *link.Link that the adapter uses.
type linkAPI interface {
	Send(peer link.Fingerprint, f link.Frame) error
	Request(ctx context.Context, peer link.Fingerprint, f link.Frame) (reply link.Frame, sent bool, err error)
	HandleState(kind link.FrameKind, h func(peer link.Fingerprint, f link.Frame))
	HandleRequest(kind link.FrameKind, h func(ctx context.Context, peer link.Fingerprint, f link.Frame) link.Frame)
	Subscribe(onUp func(peer link.Fingerprint, salt []byte), onDown func(peer link.Fingerprint))
	State(peer link.Fingerprint) link.LinkState
}

// linkTransport implements Links on top of the link package. It converts the
// directory's kind-and-payload frames to and from link.Frame; no linkpb frame is
// built here.
type linkTransport struct {
	api linkAPI
}

var _ Links = (*linkTransport)(nil)

// newLinkTransport wraps the link API in the directory's Links interface.
func newLinkTransport(api linkAPI) Links {
	return &linkTransport{api: api}
}

// Send converts the frame and forwards link's error unchanged.
func (t *linkTransport) Send(peer Fingerprint, kind uint32, payload []byte) error {
	return t.api.Send(peer, link.Frame{Kind: kind, Payload: payload})
}

// Request converts the frame and forwards the reply. An unsupported request is
// reported by link as *link.UnsupportedError and is returned as is.
func (t *linkTransport) Request(ctx context.Context, peer Fingerprint, kind uint32, payload []byte) (replyKind uint32, reply []byte, sent bool, err error) {
	f, sent, err := t.api.Request(ctx, peer, link.Frame{Kind: kind, Payload: payload})
	if err != nil {
		return 0, nil, sent, err
	}
	return f.Kind, f.Payload, sent, nil
}

// HandleState unwraps the link frame before calling h.
func (t *linkTransport) HandleState(kind uint32, h func(peer Fingerprint, payload []byte)) {
	t.api.HandleState(kind, func(peer link.Fingerprint, f link.Frame) {
		h(peer, f.Payload)
	})
}

// HandleRequest wraps h's reply in a link frame.
func (t *linkTransport) HandleRequest(kind uint32, h func(ctx context.Context, peer Fingerprint, payload []byte) (replyKind uint32, reply []byte)) {
	t.api.HandleRequest(kind, func(ctx context.Context, peer link.Fingerprint, f link.Frame) link.Frame {
		replyKind, reply := h(ctx, peer, f.Payload)
		return link.Frame{Kind: replyKind, Payload: reply}
	})
}

// Subscribe forwards the link up and down notifications unchanged.
func (t *linkTransport) Subscribe(onUp func(peer Fingerprint, salt []byte), onDown func(peer Fingerprint)) {
	t.api.Subscribe(onUp, onDown)
}

// Status converts the link's state into the directory's view.
func (t *linkTransport) Status(peer Fingerprint) LinkView {
	return mapLinkState(t.api.State(peer))
}

// mapLinkState converts a link state into the directory's LinkView. Phases map
// one to one by name; a failure reason without a directory counterpart becomes
// FailureNone. PeerVersion is carried over unchanged.
func mapLinkState(st link.LinkState) LinkView {
	v := LinkView{PeerVersion: st.PeerVersion}
	switch st.Phase {
	case link.PhaseNone:
		v.Phase = LinkNone
	case link.PhaseDialing:
		v.Phase = LinkDialing
	case link.PhaseWaiting:
		v.Phase = LinkWaiting
	case link.PhaseHandshaking:
		v.Phase = LinkHandshaking
	case link.PhaseEstablished:
		v.Phase = LinkEstablished
	case link.PhaseBackoff:
		v.Phase = LinkBackoff
	case link.PhaseRejected:
		v.Phase = LinkRejected
	default:
		v.Phase = LinkNone
	}
	switch st.LastFailure {
	case link.FailNotAPeer:
		v.Reason = FailureNotAPeer
	case link.FailRoleMismatch:
		v.Reason = FailureRoleMismatch
	case link.FailVersionIncompatible:
		v.Reason = FailureVersionIncompatible
	default:
		v.Reason = FailureNone
	}
	return v
}

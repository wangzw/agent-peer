package broker

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/config"
)

// auditStreamStub is the smallest grpc.ServerStreamingServer these tests need.
// It records every message Audit sends and can fail a chosen Send call.
type auditStreamStub struct {
	ctx    context.Context
	cancel context.CancelFunc
	sent   []*localpb.AuditEvent
	failAt int // 1-based Send call that fails; 0: none
	sends  int
	err    error
}

var _ grpc.ServerStreamingServer[localpb.AuditEvent] = (*auditStreamStub)(nil)

func (s *auditStreamStub) Send(ev *localpb.AuditEvent) error {
	s.sends++
	if s.failAt != 0 && s.sends == s.failAt {
		return s.err
	}
	s.sent = append(s.sent, ev)
	return nil
}

func (s *auditStreamStub) SetHeader(metadata.MD) error  { return nil }
func (s *auditStreamStub) SendHeader(metadata.MD) error { return nil }
func (s *auditStreamStub) SetTrailer(metadata.MD)       {}
func (s *auditStreamStub) Context() context.Context     { return s.ctx }
func (s *auditStreamStub) SendMsg(any) error            { return nil }
func (s *auditStreamStub) RecvMsg(any) error            { return nil }

// auditTestEvents returns five events one minute apart, oldest first.
func auditTestEvents() []config.Event {
	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	events := make([]config.Event, 5)
	for i := range events {
		events[i] = config.Event{
			Time:    base.Add(time.Duration(i) * time.Minute),
			Event:   config.EventConfigSet,
			Actor:   fmt.Sprintf("agent-%d", i+1),
			Account: "alice",
			Key:     "broker.alias",
			Detail:  fmt.Sprintf("detail-%d", i+1),
		}
	}
	return events
}

func newAuditStream(t *testing.T) *auditStreamStub {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &auditStreamStub{ctx: ctx, cancel: cancel}
}

// TestAuditStreamsInOrder covers REQ-broker-config-009: the audit stream
// returns the whole log in time order.
func TestAuditStreamsInOrder(t *testing.T) {
	tb := newTestBroker(t)
	events := auditTestEvents()
	tb.cfg.audits = events

	stream := newAuditStream(t)
	if err := tb.b.Audit(&localpb.AuditRequest{}, stream); err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if len(stream.sent) != len(events) {
		t.Fatalf("sent %d events, want %d", len(stream.sent), len(events))
	}
	for i, got := range stream.sent {
		if !got.Time.AsTime().Equal(events[i].Time) {
			t.Errorf("event %d time = %v, want %v", i, got.Time.AsTime(), events[i].Time)
		}
		if got.Actor != events[i].Actor {
			t.Errorf("event %d actor = %q, want %q", i, got.Actor, events[i].Actor)
		}
		if got.Event != localpb.AuditEventKind_AUDIT_EVENT_KIND_CONFIG_SET {
			t.Errorf("event %d kind = %v, want CONFIG_SET", i, got.Event)
		}
	}
}

// TestAuditLimitKeepsNewest covers REQ-broker-config-009: a limit keeps the
// newest records, still sent in time order.
func TestAuditLimitKeepsNewest(t *testing.T) {
	tb := newTestBroker(t)
	events := auditTestEvents()
	tb.cfg.audits = events

	stream := newAuditStream(t)
	if err := tb.b.Audit(&localpb.AuditRequest{Limit: 2}, stream); err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if len(stream.sent) != 2 {
		t.Fatalf("sent %d events, want 2", len(stream.sent))
	}
	want := events[3:]
	for i, got := range stream.sent {
		if got.Detail != want[i].Detail {
			t.Errorf("event %d detail = %q, want %q (newest kept, oldest first)", i, got.Detail, want[i].Detail)
		}
	}
}

func TestAuditLimitLargerThanLog(t *testing.T) {
	tb := newTestBroker(t)
	events := auditTestEvents()
	tb.cfg.audits = events

	stream := newAuditStream(t)
	if err := tb.b.Audit(&localpb.AuditRequest{Limit: 10}, stream); err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if len(stream.sent) != len(events) {
		t.Fatalf("sent %d events, want %d", len(stream.sent), len(events))
	}
}

func TestAuditSince(t *testing.T) {
	tb := newTestBroker(t)
	events := auditTestEvents()
	tb.cfg.audits = events

	stream := newAuditStream(t)
	req := &localpb.AuditRequest{Since: timestamppb.New(events[2].Time)}
	if err := tb.b.Audit(req, stream); err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if len(stream.sent) != 3 {
		t.Fatalf("sent %d events, want 3", len(stream.sent))
	}
	for i, got := range stream.sent {
		if got.Detail != events[i+2].Detail {
			t.Errorf("event %d detail = %q, want %q", i, got.Detail, events[i+2].Detail)
		}
	}
}

func TestAuditReadError(t *testing.T) {
	tb := newTestBroker(t)
	tb.cfg.readAuditErr = errors.New("disk on fire")

	stream := newAuditStream(t)
	err := tb.b.Audit(&localpb.AuditRequest{}, stream)
	detail := errorDetailOf(t, err)
	if detail.Code != localpb.ErrorCode_ERROR_CODE_INTERNAL {
		t.Errorf("code = %v, want INTERNAL", detail.Code)
	}
	if want := "reading the audit log failed: disk on fire"; detail.Message != want {
		t.Errorf("message = %q, want %q", detail.Message, want)
	}
	if stream.sends != 0 {
		t.Errorf("Send called %d times, want 0", stream.sends)
	}
}

func TestAuditSendError(t *testing.T) {
	tb := newTestBroker(t)
	tb.cfg.audits = auditTestEvents()

	sentinel := errors.New("stream broke")
	stream := newAuditStream(t)
	stream.failAt = 2
	stream.err = sentinel

	err := tb.b.Audit(&localpb.AuditRequest{}, stream)
	if !errors.Is(err, sentinel) {
		t.Fatalf("Audit error = %v, want %v", err, sentinel)
	}
	if stream.sends != 2 {
		t.Errorf("Send called %d times, want 2 (stop on the failing call)", stream.sends)
	}
	if len(stream.sent) != 1 {
		t.Errorf("recorded %d events, want 1", len(stream.sent))
	}
}

//go:build e2e

package main

import (
	"bytes"
	"fmt"
	"sync"

	"google.golang.org/protobuf/proto"

	"github.com/wangzw/peers/internal/directory/directorypb"
)

// recordSet holds the directory records the fake peer serves. The records
// command replaces the whole set.
type recordSet struct {
	mu   sync.Mutex
	recs []*directorypb.Record
}

// set replaces every record under the lock.
func (s *recordSet) set(recs []*directorypb.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recs = append([]*directorypb.Record(nil), recs...)
}

// all returns the whole record set, without the body parse that list performs.
func (s *recordSet) all() []*directorypb.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*directorypb.Record(nil), s.recs...)
}

// list returns the version list a peer uses to find the records it is missing.
// A record whose raw body does not parse is skipped.
func (s *recordSet) list() *directorypb.RecordList {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := &directorypb.RecordList{Records: make([]*directorypb.RecordVersion, 0, len(s.recs))}
	for _, rec := range s.recs {
		body := new(directorypb.RecordBody)
		if err := proto.Unmarshal(rec.GetRaw(), body); err != nil {
			continue
		}
		out.Records = append(out.Records, &directorypb.RecordVersion{
			Id: &directorypb.RecordId{
				Publisher: append([]byte(nil), body.GetPublisher()...),
				Kind:      body.GetKind(),
				Object:    body.GetObject(),
			},
			Version: body.GetVersion(),
		})
	}
	return out
}

// answer returns the records whose identifier matches one of the requested
// identifiers. The raw bytes and the signature are passed through unchanged.
func (s *recordSet) answer(req *directorypb.RecordRequest) *directorypb.Records {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := &directorypb.Records{}
	for _, rec := range s.recs {
		body := new(directorypb.RecordBody)
		if err := proto.Unmarshal(rec.GetRaw(), body); err != nil {
			continue
		}
		for _, id := range req.GetRecords() {
			if id.GetKind() != body.GetKind() || id.GetObject() != body.GetObject() {
				continue
			}
			if bytes.Equal(id.GetPublisher(), body.GetPublisher()) {
				out.Records = append(out.Records, rec)
				break
			}
		}
	}
	return out
}

// marshal encodes m. Every message this program sends has a fixed shape, so an
// encoding failure is a programming mistake and panics instead of producing an
// empty payload.
func marshal(m proto.Message) []byte {
	b, err := proto.Marshal(m)
	if err != nil {
		panic(fmt.Sprintf("fakepeer: marshal %T: %v", m, err))
	}
	return b
}

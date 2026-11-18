package directory

import (
	"bytes"
	"sort"
	"time"

	"github.com/wangzw/agent-peer/internal/directory/directorypb"
)

// limits holds the three record count limits enforced by the store. The
// per-publisher limit is the directory.max_topics_per_publisher setting; it
// bounds the public topic declarations and the records of kinds this version
// does not understand that one publisher may place in the directory. Broker
// cards and revocations are not counted.
type limits struct {
	maxRecords, maxPerLink, maxTopicsPerPublisher int
}

// acceptOutcome reports what accept did with a record.
type acceptOutcome uint8

const (
	// outcomeAccepted means the record introduced a new identifier.
	outcomeAccepted acceptOutcome = iota + 1
	// outcomeReplaced means the record replaced a lower version of the same
	// identifier.
	outcomeReplaced
	// outcomeStale means the version was not higher than the record already
	// held for that identifier.
	outcomeStale
	// outcomeExpired means the record's expiry is at or before now.
	outcomeExpired
	// outcomeTooFar means the record's expiry lies beyond the allowed window,
	// so it is treated as forged or as the product of a broken clock.
	outcomeTooFar
	// outcomeQuotaTotal means the total record limit would be exceeded.
	outcomeQuotaTotal
	// outcomeQuotaLink means the per-link record limit would be exceeded.
	outcomeQuotaLink
	// outcomeQuotaPublisher means the per-publisher limit would be exceeded.
	// Public topic declarations and records of kinds this version does not
	// understand count, so the limit cannot be bypassed with another kind.
	// Broker cards and revocations do not count.
	outcomeQuotaPublisher
)

// store is the in-memory directory. It is not safe for concurrent use; the
// owning Directory calls every method under its lock.
type store struct {
	records       map[recordID]*storedRecord
	perLink       map[Fingerprint]int // records with a non-zero source
	perPublisher  map[Fingerprint]int // records counted toward the per-publisher limit
	highestIssued uint64
	self          Fingerprint
}

// newStore returns an empty store for the local Broker identified by self.
func newStore(self Fingerprint) *store {
	return &store{
		records:      make(map[recordID]*storedRecord),
		perLink:      make(map[Fingerprint]int),
		perPublisher: make(map[Fingerprint]int),
		self:         self,
	}
}

// accept applies the acceptance rules to a record received from a link and
// stores it when the rules allow. The same identifier keeps only the highest
// version; a replacement is never subject to the count limits, so a renewal is
// accepted even when the directory is full.
func (s *store) accept(r *storedRecord, now time.Time, lim limits) acceptOutcome {
	if isExpired(r.expires, now) {
		return outcomeExpired
	}
	if expiryTooFar(r.expires, now) {
		return outcomeTooFar
	}
	if old, ok := s.records[r.id]; ok {
		if r.version <= old.version {
			return outcomeStale
		}
		s.uncount(old)
		s.put(r)
		s.raise(r)
		return outcomeReplaced
	}
	if len(s.records) >= lim.maxRecords {
		return outcomeQuotaTotal
	}
	if !r.source.IsZero() && s.perLink[r.source] >= lim.maxPerLink {
		return outcomeQuotaLink
	}
	if countsTowardPublisher(r) && s.perPublisher[r.id.publisher] >= lim.maxTopicsPerPublisher {
		return outcomeQuotaPublisher
	}
	s.put(r)
	s.raise(r)
	return outcomeAccepted
}

// putOwn stores a locally issued record. It does not enforce the count limits,
// but the record still counts toward them. highestIssued is raised to the
// record's version.
func (s *store) putOwn(r *storedRecord) {
	if old, ok := s.records[r.id]; ok {
		s.uncount(old)
	}
	s.put(r)
	s.raise(r)
}

// get returns the stored record for id.
func (s *store) get(id recordID) (*storedRecord, bool) {
	r, ok := s.records[id]
	return r, ok
}

// versions returns the identifiers and versions of every unexpired record,
// ordered by publisher, kind and object. It is the body of a full RecordList.
func (s *store) versions(now time.Time) []*directorypb.RecordVersion {
	live := make([]*storedRecord, 0, len(s.records))
	for _, r := range s.records {
		if !isExpired(r.expires, now) {
			live = append(live, r)
		}
	}
	sortRecords(live)
	out := make([]*directorypb.RecordVersion, 0, len(live))
	for _, r := range live {
		out = append(out, &directorypb.RecordVersion{
			Id: &directorypb.RecordId{
				Publisher: append([]byte(nil), r.id.publisher[:]...),
				Kind:      r.id.kind,
				Object:    r.id.object,
			},
			Version: r.version,
		})
	}
	return out
}

// purgeExpired removes every expired record and returns how many it removed.
func (s *store) purgeExpired(now time.Time) int {
	n := 0
	for _, r := range s.records {
		if isExpired(r.expires, now) {
			s.uncount(r)
			n++
		}
	}
	return n
}

// all returns every record, ordered by publisher, kind and object. The caller
// must not modify the records.
func (s *store) all() []*storedRecord {
	out := make([]*storedRecord, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	sortRecords(out)
	return out
}

// ownRecords returns the records published by the local Broker, ordered by
// publisher, kind and object. The caller must not modify the records.
func (s *store) ownRecords() []*storedRecord {
	out := make([]*storedRecord, 0)
	for _, r := range s.records {
		if r.id.publisher == s.self {
			out = append(out, r)
		}
	}
	sortRecords(out)
	return out
}

// countOwnAnnouncements returns how many live public topic declarations the
// local Broker holds, excluding the declaration named exceptName. Revocations
// share the identifier of the declaration they revoke, so a revoked
// declaration is not counted.
func (s *store) countOwnAnnouncements(now time.Time, exceptName string) int {
	n := 0
	for _, r := range s.records {
		if r.id.publisher != s.self || r.id.object == exceptName {
			continue
		}
		if isExpired(r.expires, now) || !isAnnouncement(r) {
			continue
		}
		n++
	}
	return n
}

// load replaces the store with the given records. Expired records are dropped
// and one identifier keeps its highest version, but the count limits are not
// applied: the limits may have been lowered since the file was written.
// highestIssued is the larger of the file's value and the highest version among
// the loaded local records. It returns how many records it dropped.
func (s *store) load(recs []*storedRecord, highest uint64, now time.Time) int {
	s.records = make(map[recordID]*storedRecord)
	s.perLink = make(map[Fingerprint]int)
	s.perPublisher = make(map[Fingerprint]int)
	s.highestIssued = highest
	dropped := 0
	for _, r := range recs {
		if r == nil || isExpired(r.expires, now) {
			dropped++
			continue
		}
		if old, ok := s.records[r.id]; ok {
			if r.version <= old.version {
				dropped++
				continue
			}
			s.uncount(old)
		}
		s.put(r)
		s.raise(r)
	}
	return dropped
}

// put adds a record that is known to be absent and adjusts the counters.
func (s *store) put(r *storedRecord) {
	s.records[r.id] = r
	if !r.source.IsZero() {
		s.perLink[r.source]++
	}
	if countsTowardPublisher(r) {
		s.perPublisher[r.id.publisher]++
	}
}

// uncount removes a record that is known to be present and adjusts the
// counters.
func (s *store) uncount(r *storedRecord) {
	delete(s.records, r.id)
	if !r.source.IsZero() {
		s.perLink[r.source]--
	}
	if countsTowardPublisher(r) {
		s.perPublisher[r.id.publisher]--
	}
}

// countsTowardPublisher reports whether r counts against the per-publisher
// limit. Public topic declarations and records of kinds this version does not
// understand count, so the limit cannot be bypassed with another kind. Broker
// cards and revocations do not: a card lets a Broker publish at all, and a
// revocation only withdraws the record it names.
func countsTowardPublisher(r *storedRecord) bool {
	return r.id.kind != directorypb.RecordKind_RECORD_KIND_BROKER_CARD && !r.isRevocation()
}

// raise raises highestIssued when r was published by the local Broker.
func (s *store) raise(r *storedRecord) {
	if r.id.publisher == s.self && r.version > s.highestIssued {
		s.highestIssued = r.version
	}
}

// isAnnouncement reports whether the record's payload is a public topic
// declaration. A revocation carries the identifier of the record it revokes but
// a Revocation payload, so it is not counted as a declaration.
func isAnnouncement(r *storedRecord) bool {
	return r.body != nil && r.body.GetTopicAnnouncement() != nil
}

// sortRecords orders records by publisher, then kind, then object, so that
// listings, persistence and full reconciliation lists are deterministic.
func sortRecords(recs []*storedRecord) {
	sort.Slice(recs, func(i, j int) bool {
		a, b := recs[i], recs[j]
		if c := bytes.Compare(a.id.publisher[:], b.id.publisher[:]); c != 0 {
			return c < 0
		}
		if a.id.kind != b.id.kind {
			return a.id.kind < b.id.kind
		}
		return a.id.object < b.id.object
	})
}

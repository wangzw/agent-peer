package broker

import (
	"time"

	"google.golang.org/grpc"

	"github.com/wangzw/peers/internal/broker/localpb"
)

// Audit streams audit records in time order and then returns; it does not
// follow new events. A nil since returns every record. A non-zero limit
// returns the newest limit records, still oldest first.
func (b *Broker) Audit(req *localpb.AuditRequest, stream grpc.ServerStreamingServer[localpb.AuditEvent]) error {
	since := time.Time{}
	if req.Since != nil {
		since = req.Since.AsTime()
	}

	events, err := b.deps.Config.ReadAudit(since)
	if err != nil {
		return toStatus(Errorf(localpb.ErrorCode_ERROR_CODE_INTERNAL, "reading the audit log failed: %v", err))
	}
	if limit := req.Limit; limit > 0 && uint64(len(events)) > uint64(limit) {
		events = events[len(events)-int(limit):]
	}
	for _, e := range events {
		if err := stream.Send(toAuditEvent(e)); err != nil {
			return err
		}
	}
	return nil
}

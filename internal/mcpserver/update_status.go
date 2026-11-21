package mcpserver

import (
	"context"
	"errors"

	"google.golang.org/grpc"

	"github.com/wangzw/peers/internal/broker/localpb"
)

// errNoBrokerConnection means the hint tried to query before the background
// loop had a usable connection.
var errNoBrokerConnection = errors.New("the Broker connection is not ready")

// brokerUpdateStatus returns a source that reads the connection from the status
// store at call time, so the query uses whichever Broker the loop is connected
// to when the first tool call arrives.
func brokerUpdateStatus(st *statusStore) updateStatusSource {
	return func(ctx context.Context) (updateStatus, error) {
		conn := st.Get().Conn
		if conn == nil {
			return updateStatus{}, errNoBrokerConnection
		}
		return queryUpdateStatus(ctx, conn)
	}
}

// queryUpdateStatus calls UpdateStatus on the Broker through the generated
// local client. Every peers Broker implements UpdateStatus; any error,
// including Unimplemented, is a failed query that the hint skips.
func queryUpdateStatus(ctx context.Context, conn *grpc.ClientConn) (updateStatus, error) {
	resp, err := localpb.NewLocalClient(conn).UpdateStatus(ctx, &localpb.UpdateStatusRequest{})
	if err != nil {
		return updateStatus{}, err
	}
	return updateStatus{
		CurrentVersion:   resp.GetCurrentVersion(),
		AvailableVersion: resp.GetAvailableVersion(),
		ReleaseURL:       resp.GetReleaseUrl(),
	}, nil
}

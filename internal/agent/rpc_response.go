package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"vibemonitor/pkg/protocol"
)

const (
	maxRPCResponseBytes = 1 << 20
	maxRPCErrorBytes    = 4 << 10
	errorLogInterval    = 30 * time.Second
)

// Closing a partial oversized response deliberately gives up connection reuse.
// Draining an untrusted body without a limit can consume arbitrary memory/time.
func readRPCResponse(resp *http.Response) (*protocol.Response, error) {
	defer resp.Body.Close()
	limit := maxRPCResponseBytes
	if resp.StatusCode != http.StatusOK {
		limit = maxRPCErrorBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		suffix := ""
		if len(body) > limit {
			body = body[:limit]
			suffix = " [truncated]"
		}
		return nil, fmt.Errorf("server returned status %d: %s%s", resp.StatusCode, body, suffix)
	}
	if len(body) > limit {
		return nil, fmt.Errorf("RPC response exceeds %d bytes", limit)
	}
	var rpcResp protocol.Response
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, err
	}
	if rpcResp.Error != nil {
		message := rpcResp.Error.Message
		if len(message) > maxRPCErrorBytes {
			message = message[:maxRPCErrorBytes] + " [truncated]"
		}
		return nil, fmt.Errorf("RPC error (%d): %s", rpcResp.Error.Code, message)
	}
	return &rpcResp, nil
}

// repeatedErrorLog is used by a single reporting loop. A changed error remains
// immediately visible; identical failures emit a summary every thirty seconds.
type repeatedErrorLog struct {
	last       string
	lastAt     time.Time
	suppressed int
}

func (l *repeatedErrorLog) message(err error, now time.Time) (string, bool) {
	message := err.Error()
	if message == l.last && !l.lastAt.IsZero() && now.Sub(l.lastAt) < errorLogInterval {
		l.suppressed++
		return "", false
	}
	if message == l.last && l.suppressed > 0 {
		message = fmt.Sprintf("%s (suppressed %d repeated failures)", message, l.suppressed)
	}
	l.last, l.lastAt, l.suppressed = err.Error(), now, 0
	return message, true
}

func (l *repeatedErrorLog) reset() { *l = repeatedErrorLog{} }

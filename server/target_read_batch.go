package server

import (
	"io"
	"net"
	"time"
)

const targetReadIdleWindow = 50 * time.Millisecond

// The caller has already read the first bytes without a batching deadline.
// Append into the same buffer until full, EOF/error, or 50ms without read
// progress. This is an idle window, NOT a total batch latency limit.
// Only the request proxy goroutine reads this connection or sets read deadlines.
func fillTargetRead(c net.Conn, buf []byte, n int) (total int, result error) {
	total = n
	if total == len(buf) {
		return
	}
	defer func() {
		// Never leave the batching timeout on the next initial read.
		if err := c.SetReadDeadline(time.Time{}); result == nil {
			result = err
		}
	}()
	for total < len(buf) {
		if err := c.SetReadDeadline(time.Now().Add(targetReadIdleWindow)); err != nil {
			return total, err
		}
		count, err := c.Read(buf[total:])
		total += count
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				// Timeout ends only this batch; it does not close the request.
				return total, nil
			}
			// Caller forwards the bytes before processing EOF or connection failure.
			return total, err
		}
		if count == 0 {
			return total, io.ErrNoProgress
		}
	}
	return total, nil
}

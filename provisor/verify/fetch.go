package verify

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

const (
	// fetchTimeout bounds one fetch end to end. It is enforced through the
	// request context rather than http.Client.Timeout, so a caller-supplied
	// client that left Timeout at its zero value (meaning none) still gets a
	// bound; relying on the caller to have set it would make the limit
	// optional in practice.
	fetchTimeout = 10 * time.Second

	// maxResponseBytes caps every fetch. It is enforced by capping the
	// reader itself, so an oversized body is never buffered in full before
	// being rejected.
	maxResponseBytes = 1 << 20

	// minTransferRate is the sustained floor, in bytes per second, below
	// which a transfer is judged stalled rather than merely slow. It is
	// checked only after rateGraceWindow has elapsed, so a small document
	// delivered in one fast read is never penalized for looking "slow" over
	// a near-zero duration.
	minTransferRate = 1024
	rateGraceWindow = 150 * time.Millisecond

	// maxClockSkew bounds how far the HTTP Date header on a response may
	// diverge from the verifier's own clock before every expiry check that
	// clock performs becomes untrustworthy.
	maxClockSkew = 5 * time.Minute
)

var errTransferTooSlow = errors.New("verify: transfer rate fell below the floor")

// fetch retrieves url under every transfer limit the chain requires: a total
// timeout, a response size cap enforced while reading, a sustained transfer
// rate floor, and a cross-check of the response's Date header against now.
// Timing for the rate floor uses the real clock deliberately: it measures an
// actual network transfer in progress, which elapses in real time regardless
// of what now reports, so it cannot be driven by now without ceasing to
// measure anything real. now governs every other decision in this package.
func fetch(ctx context.Context, client *http.Client, url string, now func() time.Time) ([]byte, Reason) {
	reqCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, ReasonTransferFailed
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, ReasonTransferFailed
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, ReasonTransferFailed
	}

	if dateHeader := resp.Header.Get("Date"); dateHeader != "" {
		serverTime, err := http.ParseTime(dateHeader)
		if err != nil {
			return nil, ReasonClockSkew
		}
		if skew := serverTime.Sub(now()); skew > maxClockSkew || skew < -maxClockSkew {
			return nil, ReasonClockSkew
		}
	}

	body, reason := readLimited(resp.Body)
	if reason != "" {
		return nil, reason
	}
	return body, ""
}

func readLimited(r io.Reader) ([]byte, Reason) {
	limited := &rateLimitedReader{r: io.LimitReader(r, maxResponseBytes+1), start: time.Now()}

	body, err := io.ReadAll(limited)
	switch {
	case errors.Is(err, errTransferTooSlow):
		return nil, ReasonTransferTooSlow
	case err != nil:
		return nil, ReasonTransferFailed
	case int64(len(body)) > maxResponseBytes:
		return nil, ReasonTransferTooLarge
	}
	return body, ""
}

// rateLimitedReader aborts a read once the sustained transfer rate since
// start has fallen below minTransferRate, past an initial grace window.
type rateLimitedReader struct {
	r     io.Reader
	start time.Time
	read  int64
}

func (rr *rateLimitedReader) Read(p []byte) (int, error) {
	n, err := rr.r.Read(p)
	rr.read += int64(n)

	if elapsed := time.Since(rr.start); elapsed > rateGraceWindow {
		minExpected := int64(minTransferRate * elapsed.Seconds())
		if rr.read < minExpected {
			return n, errTransferTooSlow
		}
	}
	return n, err
}

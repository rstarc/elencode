// Package retry holds the parts of "is this failure worth sending again" that
// do not depend on which API answered. Both providers switch their SDK's own
// retrying off and judge for themselves, and both SDKs judge an HTTP failure
// the same way, so keeping one copy here is what stops the two from drifting.
// Unwrapping the vendor's error type, and any error names only one API uses,
// stay with the provider.
package retry

import (
	"net/http"
	"strconv"
	"time"
)

// HeaderOverride reports what x-should-retry said, and whether it said
// anything. It is the API answering the question outright, so a caller consults
// it before judging for itself — in both directions.
func HeaderOverride(resp *http.Response) (retry bool, ok bool) {
	if resp == nil {
		return false, false
	}
	switch resp.Header.Get("x-should-retry") {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// RetryableStatus reports whether a status is one another identical request
// could get past. Everything else — a rejected prompt, a bad key — would fail
// the same way again, and retrying only delays the explanation.
func RetryableStatus(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusConflict ||
		status == http.StatusTooManyRequests ||
		status >= http.StatusInternalServerError
}

// After reads how long the API asked us to wait. Zero means it said nothing and
// the caller should fall back to its own backoff.
func After(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	// Milliseconds first: it is the more precise of the two when both are sent.
	if ms, err := strconv.Atoi(resp.Header.Get("Retry-After-Ms")); err == nil && ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return 0
}

package retry

import (
	"net/http"
	"testing"
	"time"
)

// The set matches what both SDKs retry when left to themselves. Diverging would
// mean this build gives up on failures a vendor considers transient.
func TestRetryableStatusCoversWhatTheSDKsRetry(t *testing.T) {
	for _, status := range []int{
		http.StatusRequestTimeout,
		http.StatusConflict,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			if !RetryableStatus(status) {
				t.Errorf("RetryableStatus(%d) = false, want true", status)
			}
		})
	}
}

func TestRetryableStatusLeavesPermanentFailuresUnmarked(t *testing.T) {
	for _, status := range []int{
		http.StatusOK,
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusNotFound,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			if RetryableStatus(status) {
				t.Errorf("RetryableStatus(%d) = true, want false", status)
			}
		})
	}
}

func TestHeaderOverrideReadsBothDirections(t *testing.T) {
	for _, test := range []struct {
		header string
		want   bool
	}{
		{header: "true", want: true},
		{header: "false", want: false},
	} {
		t.Run(test.header, func(t *testing.T) {
			got, ok := HeaderOverride(response(map[string]string{"x-should-retry": test.header}))
			if !ok {
				t.Fatalf("ok = false, want the %q header read", test.header)
			}
			if got != test.want {
				t.Errorf("HeaderOverride = %t, want %t", got, test.want)
			}
		})
	}
}

// Nothing said is not the same as "do not retry": the caller falls back to
// judging the failure for itself.
func TestHeaderOverrideReportsSilence(t *testing.T) {
	for _, test := range []struct {
		name string
		resp *http.Response
	}{
		{name: "no response", resp: nil},
		{name: "no header", resp: response(nil)},
		{name: "unknown value", resp: response(map[string]string{"x-should-retry": "maybe"})},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, ok := HeaderOverride(test.resp); ok {
				t.Error("ok = true, want the caller left to judge for itself")
			}
		})
	}
}

func TestAfterReadsRetryAfter(t *testing.T) {
	if got := After(response(map[string]string{"Retry-After": "9"})); got != 9*time.Second {
		t.Errorf("After = %s, want 9s", got)
	}
}

func TestAfterPrefersMilliseconds(t *testing.T) {
	got := After(response(map[string]string{"Retry-After-Ms": "2500", "Retry-After": "9"}))
	if got != 2500*time.Millisecond {
		t.Errorf("After = %s, want the finer-grained header to win", got)
	}
}

// Zero means the API asked for no particular wait, which is the caller's cue to
// fall back to its own backoff.
func TestAfterReportsNoHint(t *testing.T) {
	for _, test := range []struct {
		name string
		resp *http.Response
	}{
		{name: "no response", resp: nil},
		{name: "no header", resp: response(nil)},
		{name: "unparseable", resp: response(map[string]string{"Retry-After": "in a bit"})},
		{name: "not positive", resp: response(map[string]string{"Retry-After-Ms": "0"})},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := After(test.resp); got != 0 {
				t.Errorf("After = %s, want 0", got)
			}
		})
	}
}

func response(hdr map[string]string) *http.Response {
	resp := &http.Response{Header: http.Header{}}
	for k, v := range hdr {
		resp.Header.Set(k, v)
	}
	return resp
}

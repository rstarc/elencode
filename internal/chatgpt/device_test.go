package chatgpt

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// deviceIssuer stands in for the issuer's device-code endpoints and its token
// endpoint. polls are the answers to successive polls, by status; the last
// one repeats.
type deviceIssuer struct {
	t        *testing.T
	usercode int // status of the user-code request
	interval any // as the issuer sends it: a string, or a number
	polls    []int

	mu        sync.Mutex
	asked     []map[string]string // JSON bodies of user-code requests
	polled    []map[string]string // JSON bodies of polls
	exchanged []map[string]string // forms posted to /oauth/token
}

func newDeviceIssuer(t *testing.T, d *deviceIssuer) string {
	d.t = t
	server := httptest.NewServer(d)
	t.Cleanup(server.Close)
	return server.URL
}

func (d *deviceIssuer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")

	switch r.URL.Path {
	case "/api/accounts/deviceauth/usercode":
		d.asked = append(d.asked, decodeJSON(d.t, r))
		if d.usercode != http.StatusOK {
			w.WriteHeader(d.usercode)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"device_auth_id": "dev-1", "user_code": "ABCD-1234", "interval": d.interval})
	case "/api/accounts/deviceauth/token":
		d.polled = append(d.polled, decodeJSON(d.t, r))
		status := d.polls[min(len(d.polled), len(d.polls))-1]
		w.WriteHeader(status)
		if status == http.StatusOK {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"authorization_code": "the-code",
				"code_challenge":     "the-challenge",
				"code_verifier":      "the-verifier",
			})
		}
	case "/oauth/token":
		_ = r.ParseForm()
		form := map[string]string{}
		for key := range r.PostForm {
			form[key] = r.PostForm.Get(key)
		}
		d.exchanged = append(d.exchanged, form)
		claims := map[string]any{authClaim: map[string]any{"chatgpt_account_id": "acct"}}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token":  "access",
			"refresh_token": "refresh",
			"id_token":      fakeJWT(d.t, claims),
		})
	default:
		d.t.Errorf("unexpected request to %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func decodeJSON(t *testing.T, r *http.Request) map[string]string {
	if got := r.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("%s Content-Type = %q, want JSON", r.URL.Path, got)
	}
	body := map[string]string{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Errorf("%s body: %v", r.URL.Path, err)
	}
	return body
}

// shown records what DeviceLogin asked the user to do.
type shown struct{ url, code string }

func deviceLogin(t *testing.T, base string) (Tokens, shown, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var s shown
	tokens, err := OAuth{Issuer: base}.DeviceLogin(ctx, func(url, code string) { s = shown{url, code} })
	return tokens, s, err
}

func TestDeviceLoginShowsTheCodeAndWhereToEnterIt(t *testing.T) {
	d := &deviceIssuer{usercode: http.StatusOK, interval: "0", polls: []int{http.StatusOK}}
	base := newDeviceIssuer(t, d)

	_, s, err := deviceLogin(t, base)
	if err != nil {
		t.Fatalf("DeviceLogin: %v", err)
	}
	if s.url != base+"/codex/device" || s.code != "ABCD-1234" {
		t.Errorf("showed %+v, want the device page and the code", s)
	}
	if d.asked[0]["client_id"] != ClientID {
		t.Errorf("asked for a code with %v", d.asked[0])
	}
}

// The code the poll hands back is exchanged like the browser's, but with the
// verifier the issuer made and the device redirect.
func TestDeviceLoginExchangesWhatThePollReturns(t *testing.T) {
	d := &deviceIssuer{usercode: http.StatusOK, interval: "0", polls: []int{http.StatusOK}}
	base := newDeviceIssuer(t, d)

	tokens, _, err := deviceLogin(t, base)
	if err != nil {
		t.Fatalf("DeviceLogin: %v", err)
	}
	if tokens.AccountID != "acct" || tokens.RefreshToken != "refresh" {
		t.Errorf("tokens = %+v", tokens)
	}
	if d.polled[0]["device_auth_id"] != "dev-1" || d.polled[0]["user_code"] != "ABCD-1234" {
		t.Errorf("polled with %v", d.polled[0])
	}
	want := map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     ClientID,
		"code":          "the-code",
		"code_verifier": "the-verifier",
		"redirect_uri":  base + "/deviceauth/callback",
	}
	for key, value := range want {
		if got := d.exchanged[0][key]; got != value {
			t.Errorf("exchange %s = %q, want %q", key, got, value)
		}
	}
}

// Until the user has entered the code, the poll answers 403 or 404: that is
// waiting, not failing.
func TestDeviceLoginKeepsPollingWhileTheCodeIsPending(t *testing.T) {
	d := &deviceIssuer{usercode: http.StatusOK, interval: "1", polls: []int{http.StatusForbidden, http.StatusNotFound, http.StatusOK}}
	base := newDeviceIssuer(t, d)

	if _, _, err := deviceLogin(t, base); err != nil {
		t.Fatalf("DeviceLogin: %v", err)
	}
	if len(d.polled) != 3 {
		t.Errorf("polled %d times, want 3", len(d.polled))
	}
}

// The interval is read whether the issuer sends it as a string or as a number.
func TestDeviceLoginReadsANumericInterval(t *testing.T) {
	d := &deviceIssuer{usercode: http.StatusOK, interval: 0, polls: []int{http.StatusOK}}
	base := newDeviceIssuer(t, d)

	if _, _, err := deviceLogin(t, base); err != nil {
		t.Fatalf("DeviceLogin: %v", err)
	}
}

func TestDeviceLoginFailsOnAnUnexpectedPollAnswer(t *testing.T) {
	d := &deviceIssuer{usercode: http.StatusOK, interval: "0", polls: []int{http.StatusInternalServerError}}
	base := newDeviceIssuer(t, d)

	_, _, err := deviceLogin(t, base)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v, want the status", err)
	}
}

// Device codes are off unless the account enabled them; the caller falls back
// to the browser login on this error.
func TestDeviceLoginReportsDeviceCodesBeingOff(t *testing.T) {
	d := &deviceIssuer{usercode: http.StatusNotFound}
	base := newDeviceIssuer(t, d)

	if _, _, err := deviceLogin(t, base); !errors.Is(err, ErrDeviceCodeUnavailable) {
		t.Errorf("err = %v, want ErrDeviceCodeUnavailable", err)
	}
}

func TestDeviceLoginStopsWhenCancelled(t *testing.T) {
	d := &deviceIssuer{usercode: http.StatusOK, interval: "1", polls: []int{http.StatusForbidden}}
	base := newDeviceIssuer(t, d)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := OAuth{Issuer: base}.DeviceLogin(ctx, func(string, string) { cancel() })
		result <- err
	}()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("DeviceLogin did not return after cancellation")
	}
}

// A code nobody entered in time is its own ending, worth a plain message
// rather than a deadline from somewhere inside the poll.
func TestDeviceLoginReportsAnExpiredCode(t *testing.T) {
	d := &deviceIssuer{usercode: http.StatusOK, interval: "1", polls: []int{http.StatusForbidden}}
	base := newDeviceIssuer(t, d)
	lifetime := deviceCodeLifetime
	deviceCodeLifetime = 100 * time.Millisecond
	t.Cleanup(func() { deviceCodeLifetime = lifetime })

	if _, _, err := deviceLogin(t, base); !errors.Is(err, ErrDeviceCodeExpired) {
		t.Errorf("err = %v, want ErrDeviceCodeExpired", err)
	}
}

// The caller's own deadline is the caller's, not the code expiring.
func TestDeviceLoginPassesTheCallersDeadlineThrough(t *testing.T) {
	d := &deviceIssuer{usercode: http.StatusOK, interval: "1", polls: []int{http.StatusForbidden}}
	base := newDeviceIssuer(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := OAuth{Issuer: base}.DeviceLogin(ctx, func(string, string) {})
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrDeviceCodeExpired) {
		t.Errorf("err = %v, want the caller's deadline", err)
	}
}

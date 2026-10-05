package chatgpt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrDeviceCodeUnavailable means the issuer will not hand out a device code,
// which it does for an account that has not turned device codes on. The
// browser login still works.
var ErrDeviceCodeUnavailable = errors.New("signing in with a device code is not enabled for this account")

// ErrDeviceCodeExpired means nobody entered the code before it stopped being
// valid. A new login gets a new one.
var ErrDeviceCodeExpired = errors.New("the device code expired before it was entered")

// deviceCodeLifetime is how long a code stays valid, after which waiting on it
// is pointless. A variable so a test can wait less than fifteen minutes.
var deviceCodeLifetime = 15 * time.Minute

// defaultPollInterval is used when the issuer names none, rather than polling
// it as fast as the network allows.
const defaultPollInterval = 5 * time.Second

// DeviceLogin signs in without a browser on this machine: show hands the user
// a page and a code to enter there from any device, and DeviceLogin polls
// until they have.
func (o OAuth) DeviceLogin(ctx context.Context, show func(url, code string)) (Tokens, error) {
	codeCtx, cancel := context.WithTimeout(ctx, deviceCodeLifetime)
	defer cancel()

	device, err := o.requestDeviceCode(codeCtx)
	if err != nil {
		return Tokens{}, err
	}
	show(o.issuer()+"/codex/device", device.UserCode)

	granted, err := o.pollDeviceCode(codeCtx, device)
	// The code's own deadline passing, rather than the caller's, is the code
	// expiring.
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		return Tokens{}, ErrDeviceCodeExpired
	}
	if err != nil {
		return Tokens{}, err
	}
	// The issuer made the PKCE pair for this flow, and hands the verifier back
	// with the code.
	return o.exchange(ctx, granted.AuthorizationCode, granted.CodeVerifier, o.issuer()+"/deviceauth/callback")
}

// deviceCode is the issuer's answer to asking for a code. The interval is kept
// raw: the issuer sends it as a string, and nothing says it is never a number.
type deviceCode struct {
	DeviceAuthID string          `json:"device_auth_id"`
	UserCode     string          `json:"user_code"`
	UserCodeAlt  string          `json:"usercode"`
	Interval     json.RawMessage `json:"interval"`
}

// pollInterval reads the interval in seconds, falling back to the default for
// anything missing, zero or unreadable.
func (d deviceCode) pollInterval() time.Duration {
	seconds, err := strconv.Atoi(strings.Trim(string(d.Interval), `"`))
	if err != nil || seconds <= 0 {
		return defaultPollInterval
	}
	return time.Duration(seconds) * time.Second
}

// deviceGrant is what the poll returns once the user has entered the code.
type deviceGrant struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
}

func (o OAuth) requestDeviceCode(ctx context.Context) (deviceCode, error) {
	resp, err := o.postJSON(ctx, "/api/accounts/deviceauth/usercode", map[string]string{"client_id": ClientID})
	if err != nil {
		return deviceCode{}, fmt.Errorf("asking for a device code: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return deviceCode{}, ErrDeviceCodeUnavailable
	}
	if resp.StatusCode != http.StatusOK {
		return deviceCode{}, fmt.Errorf("asking for a device code: %s", resp.Status)
	}
	var device deviceCode
	if err := json.NewDecoder(resp.Body).Decode(&device); err != nil {
		return deviceCode{}, fmt.Errorf("reading the device code: %w", err)
	}
	if device.UserCode == "" {
		device.UserCode = device.UserCodeAlt
	}
	return device, nil
}

// pollDeviceCode waits for the user to enter the code. A 403 or 404 is the
// issuer saying they have not yet.
func (o OAuth) pollDeviceCode(ctx context.Context, device deviceCode) (deviceGrant, error) {
	body := map[string]string{"device_auth_id": device.DeviceAuthID, "user_code": device.UserCode}
	for {
		resp, err := o.postJSON(ctx, "/api/accounts/deviceauth/token", body)
		if err != nil {
			return deviceGrant{}, fmt.Errorf("waiting for the device code to be entered: %w", err)
		}

		switch resp.StatusCode {
		case http.StatusOK:
			var granted deviceGrant
			err := json.NewDecoder(resp.Body).Decode(&granted)
			_ = resp.Body.Close()
			if err != nil {
				return deviceGrant{}, fmt.Errorf("reading the device grant: %w", err)
			}
			return granted, nil
		case http.StatusForbidden, http.StatusNotFound:
			_ = resp.Body.Close()
		default:
			_ = resp.Body.Close()
			return deviceGrant{}, fmt.Errorf("waiting for the device code to be entered: %s", resp.Status)
		}

		select {
		case <-time.After(device.pollInterval()):
		case <-ctx.Done():
			return deviceGrant{}, ctx.Err()
		}
	}
}

func (o OAuth) postJSON(ctx context.Context, path string, body any) (*http.Response, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.issuer()+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

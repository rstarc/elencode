package chatgpt

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"

	"golang.org/x/oauth2"
)

// CallbackAddr is where the browser is sent back to. The port is the one
// registered for the client: the issuer refuses any other redirect.
const CallbackAddr = "127.0.0.1:1455"

const callbackPath = "/auth/callback"

// Login runs the browser sign-in: it listens on addr for the redirect, hands
// open the page to sign in on, and returns the login once the browser comes
// back with a code and it has been exchanged. Cancelling ctx gives up.
func (o OAuth) Login(ctx context.Context, addr string, open func(url string)) (Tokens, error) {
	verifier := oauth2.GenerateVerifier()
	state, err := newState()
	if err != nil {
		return Tokens{}, err
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return Tokens{}, fmt.Errorf("listening for the login callback on %s: %w", addr, err)
	}
	redirectURI := "http://" + listener.Addr().String() + callbackPath

	// Buffered for the one outcome that counts; anything after it is dropped.
	results := make(chan loginResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		tokens, err := o.callback(ctx, r, state, verifier, redirectURI)
		if err != nil {
			http.Error(w, "elencode: login failed: "+err.Error(), http.StatusBadRequest)
		} else {
			_, _ = fmt.Fprintln(w, "elencode is signed in to ChatGPT. You can close this tab.")
		}
		select {
		case results <- loginResult{tokens, err}:
		default:
		}
	})
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	// Closed here as well as by Shutdown, which only closes the listeners
	// Serve has taken over: a login given up before Serve got going would
	// otherwise hold the port until it did, and a login started over at once
	// could not listen on it.
	defer func() { _ = listener.Close() }()
	// Shutdown waits for the callback's answer to be written, so the browser
	// sees the page before the port goes away.
	defer func() { _ = server.Shutdown(context.Background()) }()

	open(o.authorizeURL(redirectURI, verifier, state))

	select {
	case result := <-results:
		return result.tokens, result.err
	case <-ctx.Done():
		return Tokens{}, ctx.Err()
	}
}

type loginResult struct {
	tokens Tokens
	err    error
}

// callback reads the issuer's redirect and, if it carries this login's state
// and a code, exchanges the code. The state is checked first: a code arriving
// with any other state is not this login's to spend.
func (o OAuth) callback(ctx context.Context, r *http.Request, state, verifier, redirectURI string) (Tokens, error) {
	query := r.URL.Query()
	if query.Get("state") != state {
		return Tokens{}, errors.New("the callback's state does not match this login")
	}
	if reason := query.Get("error"); reason != "" {
		return Tokens{}, fmt.Errorf("the issuer refused the login: %s: %s", reason, query.Get("error_description"))
	}
	code := query.Get("code")
	if code == "" {
		return Tokens{}, errors.New("the callback carried no authorization code")
	}
	return o.exchange(ctx, code, verifier, redirectURI)
}

// newState is the anti-forgery value the issuer hands back unchanged.
func newState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

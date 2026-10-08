package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/config"
	"github.com/rstarc/elencode/internal/provider/anthropic"
	"github.com/rstarc/elencode/internal/provider/openai"
)

// keyProviders are the providers connected with an API key. The rest are
// signed in to.
var keyProviders = []agent.ProviderName{agent.ProviderAnthropic, agent.ProviderOpenAI, agent.ProviderMoonshot}

// apiKeyPages are where a provider's keys are made, for the prompt that asks
// for one.
var apiKeyPages = map[agent.ProviderName]string{
	agent.ProviderAnthropic: "https://console.anthropic.com/settings/keys",
	agent.ProviderOpenAI:    "https://platform.openai.com/api-keys",
	agent.ProviderMoonshot:  "https://platform.kimi.ai/console/api-keys",
}

// providerTitles are the names a provider goes by in a sentence.
var providerTitles = map[agent.ProviderName]string{
	agent.ProviderAnthropic: "Anthropic",
	agent.ProviderOpenAI:    "OpenAI",
	agent.ProviderChatGPT:   "ChatGPT",
	agent.ProviderMoonshot:  "Moonshot",
}

// providerStatus is what a session knows about one provider: whether it is
// connected, and with what. One description serves the config view and the
// list of providers to connect, so the two cannot say different things.
type providerStatus struct {
	provider  agent.ProviderName
	connected bool
	// how says what the provider is connected with, or what connecting it
	// takes when it is not.
	how string
}

// description is the status as one line: "connected · API key in
// credentials.json".
func (s providerStatus) description() string {
	if s.connected {
		return "connected · " + s.how
	}
	return "not connected · " + s.how
}

// providerStatuses is every provider, connected or not, in the order agent
// prefers them.
func providerStatuses(cfg config.Config) []providerStatus {
	var statuses []providerStatus
	for _, provider := range agent.Providers {
		status := providerStatus{provider: provider}
		_, source := cfg.APIKey(provider)
		switch {
		case provider == agent.ProviderChatGPT && cfg.Credentials[provider].Login != nil:
			status.connected = true
			status.how = "signed in to ChatGPT"
		case provider == agent.ProviderChatGPT:
			status.how = "sign in with a ChatGPT plan"
		case source == config.KeyFromEnv:
			status.connected = true
			status.how = "API key from $" + config.APIKeyEnvVar(provider)
		case source == config.KeyFromCredentials:
			status.connected = true
			status.how = "API key in credentials.json"
		default:
			status.how = "API key"
		}
		statuses = append(statuses, status)
	}
	return statuses
}

// connectProvider reads the provider connect or disconnect was given. Shared
// by the CLI and the slash commands, so both refuse the same things the same
// way.
func connectProvider(name string) (agent.ProviderName, error) {
	var names []string
	for _, provider := range agent.Providers {
		names = append(names, string(provider))
	}
	list := strings.Join(names, ", ")

	provider := agent.ProviderName(name)
	switch {
	case name == "":
		return "", fmt.Errorf("name the provider: %s", list)
	case slices.Contains(agent.Providers, provider):
		return provider, nil
	default:
		return "", fmt.Errorf("unknown provider %q (providers: %s)", name, list)
	}
}

// parseConnectArgs reads `connect <provider> [--device]`, from the CLI's
// arguments or the words after /connect.
func parseConnectArgs(args []string) (agent.ProviderName, bool, error) {
	var names []string
	device := false
	for _, arg := range args {
		switch {
		case arg == "--device":
			device = true
		case strings.HasPrefix(arg, "-"):
			return "", false, fmt.Errorf("unknown flag %s (connect takes --device, to sign in to ChatGPT with a code)", arg)
		default:
			names = append(names, arg)
		}
	}
	provider, err := connectProvider(strings.Join(names, " "))
	if err != nil {
		return "", false, err
	}
	if device && provider != agent.ProviderChatGPT {
		return "", false, fmt.Errorf("--device is for signing in to ChatGPT: %s is connected with an API key", provider)
	}
	return provider, device, nil
}

// newKeyedProvider is the client for provider, reached with key.
func newKeyedProvider(provider agent.ProviderName, key string, cfg config.Config) agent.Provider {
	effort := agent.Effort(cfg.ThinkingEffort)
	switch provider {
	case agent.ProviderAnthropic:
		return anthropic.New(key, cfg.ThinkingEnabled, effort)
	case agent.ProviderOpenAI:
		return openai.New(key, cfg.ThinkingEnabled, effort)
	case agent.ProviderMoonshot:
		return anthropic.NewMoonshot(key, cfg.ThinkingEnabled, effort)
	}
	return nil
}

// keyCheck asks provider's API whether it accepts key. A function rather than
// a call, so tests can answer for the API.
type keyCheck func(ctx context.Context, provider agent.ProviderName, key string) error

// checkWithAPI is the key check against the real API.
func checkWithAPI(ctx context.Context, provider agent.ProviderName, key string) error {
	switch provider {
	case agent.ProviderAnthropic:
		return anthropic.New(key, false, agent.EffortNone).CheckKey(ctx)
	case agent.ProviderOpenAI:
		return openai.New(key, false, agent.EffortNone).CheckKey(ctx)
	case agent.ProviderMoonshot:
		return anthropic.NewMoonshot(key, false, agent.EffortNone).CheckKey(ctx)
	}
	return fmt.Errorf("%s is not connected with an API key", provider)
}

// keyCheckTimeout bounds the check, which an API that does not answer would
// otherwise hold up indefinitely: the key is saved unchecked when it runs out.
const keyCheckTimeout = 15 * time.Second

// connectKey checks key with provider's API and saves it to credentials.json,
// returning cfg with the key in it. A key the API rejects is not saved. One
// that could not be checked is, since that says nothing about the key, and
// unchecked says why it could not be.
func connectKey(ctx context.Context, cfg config.Config, provider agent.ProviderName, key string, check keyCheck) (connected config.Config, unchecked, err error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return cfg, nil, errors.New("no API key given")
	}

	checkCtx, cancel := context.WithTimeout(ctx, keyCheckTimeout)
	defer cancel()
	unchecked = check(checkCtx, provider, key)
	if errors.Is(unchecked, agent.ErrKeyRejected) {
		return cfg, nil, fmt.Errorf("%s: %w", provider, unchecked)
	}
	// Given up on by the user rather than timed out: nothing is to be saved
	if ctx.Err() != nil {
		return cfg, nil, ctx.Err()
	}

	creds, err := config.SaveCredential(cfg.CredentialsPath, provider, config.Credential{APIKey: config.Secret(key)})
	if err != nil {
		return cfg, nil, fmt.Errorf("saving the key to %s: %w", cfg.CredentialsPath, err)
	}
	cfg.Credentials = creds
	return cfg, unchecked, nil
}

// connectedNotes is what to say once a key is saved: where it went, whether
// it was checked, and what to do next.
func connectedNotes(cfg config.Config, provider agent.ProviderName, unchecked error) []string {
	notes := []string{fmt.Sprintf("Connected %s; the key is saved in %s.", provider, cfg.CredentialsPath)}
	if unchecked != nil {
		notes = append(notes, fmt.Sprintf("The key could not be checked (%v), so it was saved unchecked.", unchecked))
	}
	return notes
}

// environmentWins says, before a key is asked for, that the environment
// already supplies one, which would win over whatever is saved. Empty when it
// does not.
func environmentWins(cfg config.Config, provider agent.ProviderName) string {
	if _, source := cfg.APIKey(provider); source != config.KeyFromEnv {
		return ""
	}
	return fmt.Sprintf("$%s is set, and wins over any key saved here.", config.APIKeyEnvVar(provider))
}

// disconnectProvider forgets provider's credential in credentials.json,
// returning cfg without it and what to say about it. Only the local copy: a
// key is not revoked with the provider, nor a ChatGPT login with the issuer.
func disconnectProvider(cfg config.Config, provider agent.ProviderName) (config.Config, string, error) {
	// elencode cannot unset a variable in the shell that started it
	if _, source := cfg.APIKey(provider); source == config.KeyFromEnv {
		return cfg, "", fmt.Errorf("the key comes from $%s: unset it to disconnect %s", config.APIKeyEnvVar(provider), provider)
	}
	left, removed, err := config.RemoveCredential(cfg.CredentialsPath, provider)
	if err != nil {
		return cfg, "", fmt.Errorf("disconnecting %s in %s: %w", provider, cfg.CredentialsPath, err)
	}
	// Taken from the file even when it was gone already, perhaps by another
	// session: this session's copy lets go of it too
	cfg.Credentials = left
	if !removed {
		return cfg, fmt.Sprintf("%s was not connected.", provider), nil
	}
	what := "key"
	if provider == agent.ProviderChatGPT {
		what = "sign-in"
	}
	return cfg, fmt.Sprintf("Disconnected %s; removed its %s from %s.", provider, what, cfg.CredentialsPath), nil
}

// connectCLI is `elencode connect [provider] [--device]`, also `elencode
// login`. Without a provider it lists them, which is how to see from the
// shell what is connected. ctrl+c gives up on it cleanly.
func connectCLI(args []string, out io.Writer) error {
	// Not config.Load: connecting needs none of the settings, so a mistake in
	// them must not stand in its way
	cfg, err := config.LoadConnections(os.LookupEnv)
	if err != nil {
		return err
	}
	for _, warning := range cfg.Warnings {
		_, _ = fmt.Fprintf(out, "elencode: %s\n", warning)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// The panels need a terminal both ways: to draw on, and to read keys
	// from. Without one, the prompts are printed and a key is read from stdin.
	var keys io.Reader
	if isTerminal(out) && isTerminal(os.Stdin) {
		keys = os.Stdin
	}

	if len(args) == 0 {
		for _, status := range providerStatuses(cfg) {
			_, _ = fmt.Fprintf(out, "%-10s %s\n", status.provider, status.description())
		}
		return errors.New("name the provider to connect: elencode connect <provider>")
	}
	provider, device, err := parseConnectArgs(args)
	if err != nil {
		return err
	}

	if provider == agent.ProviderChatGPT {
		s := defaultSignIn(cfg.CredentialsPath)
		s.device = device
		return runLogin(ctx, s, out, keys)
	}

	var readSecret func() (string, error)
	if keys != nil {
		fd := os.Stdin.Fd()
		readSecret = func() (string, error) {
			// ctrl+c reaches ctx rather than ending the process, which would
			// leave the terminal not echoing: the read is given up on instead,
			// and the terminal put back as it was
			state, err := term.GetState(fd)
			if err != nil {
				return "", err
			}
			type read struct {
				key []byte
				err error
			}
			done := make(chan read, 1)
			go func() {
				key, err := term.ReadPassword(fd)
				done <- read{key, err}
			}()
			select {
			case r := <-done:
				return string(r.key), r.err
			case <-ctx.Done():
				_ = term.Restore(fd, state)
				return "", errConnectCancelled
			}
		}
	}
	return runConnectKey(ctx, cfg, provider, os.Stdin, readSecret, out, checkWithAPI)
}

var errConnectCancelled = errors.New("connect cancelled")

// runConnectKey is `elencode connect <provider>` for a provider reached with
// an API key. readSecret reads a line from the terminal without echoing it:
// with one, the key is asked for, and asked for again if the API rejects it.
// Without, it is read from stdin, which is how a password manager hands one
// over.
func runConnectKey(ctx context.Context, cfg config.Config, provider agent.ProviderName, stdin io.Reader, readSecret func() (string, error), out io.Writer, check keyCheck) error {
	if note := environmentWins(cfg, provider); note != "" {
		_, _ = fmt.Fprintln(out, note)
	}

	var connected config.Config
	var unchecked error
	if readSecret == nil {
		// Bounded, so a mistaken pipe of a large file is not read whole: no API
		// key comes anywhere near it
		body, err := io.ReadAll(io.LimitReader(stdin, 64<<10))
		if err != nil {
			return err
		}
		key := strings.TrimSpace(string(body))
		if key == "" {
			return fmt.Errorf("no API key on standard input: pipe one in, as in `pass show %s | elencode connect %s`", provider, provider)
		}
		connected, unchecked, err = connectKey(ctx, cfg, provider, key, check)
		if err != nil {
			return err
		}
	} else {
		for {
			_, _ = fmt.Fprintf(out, "Paste your %s API key (create one at %s): ", providerTitles[provider], apiKeyPages[provider])
			key, err := readSecret()
			// Nothing was echoed, the newline included
			_, _ = fmt.Fprintln(out)
			if err != nil {
				return err
			}
			if strings.TrimSpace(key) == "" {
				continue
			}
			connected, unchecked, err = connectKey(ctx, cfg, provider, key, check)
			if errors.Is(err, agent.ErrKeyRejected) {
				_, _ = fmt.Fprintf(out, "%v: try again, or ctrl+c to give up\n", err)
				continue
			}
			if err != nil {
				return err
			}
			break
		}
	}

	for _, note := range connectedNotes(connected, provider, unchecked) {
		_, _ = fmt.Fprintln(out, note)
	}
	_, _ = fmt.Fprintln(out, "Pick one of its models with /model, or `elencode model`.")
	return nil
}

// disconnectCLI is `elencode disconnect <provider>`, also `elencode logout`.
func disconnectCLI(args []string, out io.Writer) error {
	provider, err := connectProvider(strings.Join(args, " "))
	if err != nil {
		return err
	}
	cfg, err := config.LoadConnections(os.LookupEnv)
	if err != nil {
		return err
	}
	_, said, err := disconnectProvider(cfg, provider)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, said)
	return nil
}

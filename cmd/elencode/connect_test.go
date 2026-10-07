package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/chatgpt"
	"github.com/rstarc/elencode/internal/config"
)

func TestParseConnectArgs(t *testing.T) {
	tests := []struct {
		args     []string
		provider agent.ProviderName
		device   bool
		ok       bool
	}{
		{[]string{"chatgpt"}, agent.ProviderChatGPT, false, true},
		{[]string{"chatgpt", "--device"}, agent.ProviderChatGPT, true, true},
		{[]string{"--device", "chatgpt"}, agent.ProviderChatGPT, true, true},
		{[]string{"anthropic"}, agent.ProviderAnthropic, false, true},
		{[]string{"openai"}, agent.ProviderOpenAI, false, true},
		{nil, "", false, false},
		{[]string{"--device"}, "", false, false},
		{[]string{"chatgpt", "--browser"}, "", false, false},
		{[]string{"chatgpt", "openai"}, "", false, false},
		{[]string{"acme"}, "", false, false},
	}
	for _, test := range tests {
		provider, device, err := parseConnectArgs(test.args)
		if (err == nil) != test.ok {
			t.Errorf("parseConnectArgs(%q) err = %v, want ok = %v", test.args, err, test.ok)
			continue
		}
		if test.ok && (provider != test.provider || device != test.device) {
			t.Errorf("parseConnectArgs(%q) = %q, %v", test.args, provider, device)
		}
	}
}

// Which provider is not implied: with several to connect, a bare connect
// would have to guess, so it lists them instead.
func TestParseConnectArgsListsTheProvidersWhenNoneIsNamed(t *testing.T) {
	_, _, err := parseConnectArgs(nil)
	for _, want := range []string{"anthropic", "chatgpt", "openai"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to list %s", err, want)
		}
	}
}

// A flag it does not know is named, rather than read as a provider.
func TestParseConnectArgsNamesAnUnknownFlag(t *testing.T) {
	_, _, err := parseConnectArgs([]string{"chatgpt", "--browser"})
	if err == nil || !strings.Contains(err.Error(), "--browser") || strings.Contains(err.Error(), "provider") {
		t.Errorf("err = %v, want it to name the flag", err)
	}
}

// A device code is a way to sign in, and a key provider is not signed in to.
func TestParseConnectArgsRefusesADeviceCodeForAKeyProvider(t *testing.T) {
	_, _, err := parseConnectArgs([]string{"anthropic", "--device"})
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Errorf("err = %v, want it to say anthropic takes an API key", err)
	}
}

// fakeCheck is a key check that answers from a table rather than an API,
// remembering what it was asked. Locked: a session checks off its update loop.
type fakeCheck struct {
	answers map[string]error
	mu      sync.Mutex
	asked   []string
}

func (f *fakeCheck) check(_ context.Context, _ agent.ProviderName, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, key)
	return f.answers[key]
}

var errRejected = fmt.Errorf("%w: invalid x-api-key", agent.ErrKeyRejected)

// connectionConfig is a config whose credentials are kept in a fresh
// directory, holding creds to start with.
func connectionConfig(t *testing.T, creds config.Credentials) config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credentials.json")
	if creds != nil {
		if err := creds.Save(path); err != nil {
			t.Fatal(err)
		}
	}
	return config.Config{CredentialsPath: path, Credentials: creds}
}

func savedCredentials(t *testing.T, cfg config.Config) config.Credentials {
	t.Helper()
	creds, err := config.LoadCredentials(cfg.CredentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	return creds
}

func TestConnectKeySavesAKeyTheAPIAccepts(t *testing.T) {
	cfg := connectionConfig(t, config.Credentials{agent.ProviderOpenAI: {APIKey: "sk-oai"}})
	check := &fakeCheck{}

	connected, unchecked, err := connectKey(context.Background(), cfg, agent.ProviderAnthropic, "sk-ant", check.check)
	if err != nil || unchecked != nil {
		t.Fatalf("connectKey = %v, %v", unchecked, err)
	}
	if len(check.asked) != 1 || check.asked[0] != "sk-ant" {
		t.Errorf("checked %q, want the key", check.asked)
	}
	saved := savedCredentials(t, cfg)
	if saved[agent.ProviderAnthropic].APIKey != "sk-ant" {
		t.Error("the key was not saved")
	}
	if saved[agent.ProviderOpenAI].APIKey != "sk-oai" {
		t.Error("saving dropped another provider's key")
	}
	if key, _ := connected.APIKey(agent.ProviderAnthropic); key != "sk-ant" {
		t.Error("the returned config does not hold the key")
	}
}

// A mistyped key never reaches the file: the user is told, and asked again.
func TestConnectKeyDoesNotSaveARejectedKey(t *testing.T) {
	cfg := connectionConfig(t, nil)
	check := &fakeCheck{answers: map[string]error{"sk-typo": errRejected}}

	_, _, err := connectKey(context.Background(), cfg, agent.ProviderAnthropic, "sk-typo", check.check)
	if !errors.Is(err, agent.ErrKeyRejected) {
		t.Errorf("err = %v, want the rejection", err)
	}
	if _, statErr := os.Stat(cfg.CredentialsPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("stat = %v, want nothing written", statErr)
	}
}

// Not reaching the API says nothing about the key, so it is kept, and the
// caller is told it was not checked.
func TestConnectKeySavesAKeyItCouldNotCheck(t *testing.T) {
	cfg := connectionConfig(t, nil)
	offline := errors.New("dial tcp: no route to host")
	check := &fakeCheck{answers: map[string]error{"sk-ant": offline}}

	_, unchecked, err := connectKey(context.Background(), cfg, agent.ProviderAnthropic, "sk-ant", check.check)
	if err != nil {
		t.Fatalf("connectKey: %v", err)
	}
	if !errors.Is(unchecked, offline) {
		t.Errorf("unchecked = %v, want why the check failed", unchecked)
	}
	if savedCredentials(t, cfg)[agent.ProviderAnthropic].APIKey != "sk-ant" {
		t.Error("the unchecked key was not saved")
	}
}

func TestConnectKeyRefusesAnEmptyKey(t *testing.T) {
	check := &fakeCheck{}
	if _, _, err := connectKey(context.Background(), connectionConfig(t, nil), agent.ProviderAnthropic, "", check.check); err == nil {
		t.Error("connectKey accepted an empty key")
	}
	if len(check.asked) != 0 {
		t.Error("an empty key was sent to the API")
	}
}

func TestDisconnectProviderForgetsTheStoredKey(t *testing.T) {
	cfg := connectionConfig(t, config.Credentials{
		agent.ProviderAnthropic: {APIKey: "sk-ant"},
		agent.ProviderOpenAI:    {APIKey: "sk-oai"},
	})

	disconnected, said, err := disconnectProvider(cfg, agent.ProviderAnthropic)
	if err != nil {
		t.Fatalf("disconnectProvider: %v", err)
	}
	saved := savedCredentials(t, cfg)
	if _, ok := saved[agent.ProviderAnthropic]; ok {
		t.Error("the key is still saved")
	}
	if saved[agent.ProviderOpenAI].APIKey != "sk-oai" {
		t.Error("another provider's key went with it")
	}
	if key, _ := disconnected.APIKey(agent.ProviderAnthropic); key != "" {
		t.Error("the returned config still holds the key")
	}
	if !strings.Contains(said, "Disconnected anthropic") || !strings.Contains(said, cfg.CredentialsPath) {
		t.Errorf("said %q", said)
	}
}

// elencode cannot unset a variable in the shell that started it, so it says
// what to do instead of pretending.
func TestDisconnectKeyRefusesAKeyFromTheEnvironment(t *testing.T) {
	cfg := connectionConfig(t, config.Credentials{agent.ProviderAnthropic: {APIKey: "sk-ant"}})
	cfg.Env = func(name string) (string, bool) { return "sk-env", name == "ANTHROPIC_API_KEY" }

	_, _, err := disconnectProvider(cfg, agent.ProviderAnthropic)
	if err == nil || !strings.Contains(err.Error(), "$ANTHROPIC_API_KEY") {
		t.Errorf("err = %v, want it to name the variable", err)
	}
	if savedCredentials(t, cfg)[agent.ProviderAnthropic].APIKey != "sk-ant" {
		t.Error("the stored key was removed anyway")
	}
}

// Disconnecting twice is not a failure: the state asked for is the state
// there is.
func TestDisconnectKeyWithoutAKeySaysSo(t *testing.T) {
	_, said, err := disconnectProvider(connectionConfig(t, nil), agent.ProviderOpenAI)
	if err != nil {
		t.Fatalf("disconnectProvider: %v", err)
	}
	if !strings.Contains(said, "not connected") {
		t.Errorf("said %q", said)
	}
}

// runConnectKeyFromPipe is `elencode connect <provider>` with stdin piped in.
func runConnectKeyFromPipe(t *testing.T, cfg config.Config, provider agent.ProviderName, stdin string, check keyCheck) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runConnectKey(context.Background(), cfg, provider, strings.NewReader(stdin), nil, &out, check)
	return out.String(), err
}

// A pipe is how a key gets in from a password manager: the trailing newline
// it comes with is not part of it.
func TestConnectCLIReadsTheKeyFromAPipe(t *testing.T) {
	cfg := connectionConfig(t, nil)
	check := &fakeCheck{}

	out, err := runConnectKeyFromPipe(t, cfg, agent.ProviderAnthropic, "sk-ant\n", check.check)
	if err != nil {
		t.Fatalf("runConnectKey: %v", err)
	}
	if savedCredentials(t, cfg)[agent.ProviderAnthropic].APIKey != "sk-ant" {
		t.Error("the key was not saved as piped, without its newline")
	}
	for _, want := range []string{"Connected anthropic", cfg.CredentialsPath, "/model"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not mention %q", out, want)
		}
	}
	if strings.Contains(out, "sk-ant") {
		t.Error("the output repeats the key")
	}
}

func TestConnectCLIFailsOnARejectedKey(t *testing.T) {
	cfg := connectionConfig(t, nil)
	check := &fakeCheck{answers: map[string]error{"sk-typo": errRejected}}

	_, err := runConnectKeyFromPipe(t, cfg, agent.ProviderAnthropic, "sk-typo", check.check)
	if !errors.Is(err, agent.ErrKeyRejected) {
		t.Errorf("err = %v, want the rejection", err)
	}
}

func TestConnectCLISaysWhenTheKeyCouldNotBeChecked(t *testing.T) {
	check := &fakeCheck{answers: map[string]error{"sk-ant": errors.New("dial tcp: no route to host")}}

	out, err := runConnectKeyFromPipe(t, connectionConfig(t, nil), agent.ProviderAnthropic, "sk-ant", check.check)
	if err != nil {
		t.Fatalf("runConnectKey: %v", err)
	}
	if !strings.Contains(out, "could not be checked") || !strings.Contains(out, "no route to host") {
		t.Errorf("output %q, want it to say the key is unchecked, and why", out)
	}
}

// A key saved while the environment supplies another would never be used, and
// the user should hear that now rather than wonder later.
func TestConnectCLISaysTheEnvironmentWins(t *testing.T) {
	cfg := connectionConfig(t, nil)
	cfg.Env = func(name string) (string, bool) { return "sk-env", name == "ANTHROPIC_API_KEY" }

	out, err := runConnectKeyFromPipe(t, cfg, agent.ProviderAnthropic, "sk-ant", (&fakeCheck{}).check)
	if err != nil {
		t.Fatalf("runConnectKey: %v", err)
	}
	if !strings.Contains(out, "$ANTHROPIC_API_KEY is set") {
		t.Errorf("output %q, want it to say the variable wins", out)
	}
}

func TestConnectCLIRefusesAnEmptyPipe(t *testing.T) {
	_, err := runConnectKeyFromPipe(t, connectionConfig(t, nil), agent.ProviderAnthropic, "\n", (&fakeCheck{}).check)
	if err == nil || !strings.Contains(err.Error(), "standard input") {
		t.Errorf("err = %v, want it to say where the key was expected", err)
	}
}

// `elencode connect` on its own lists the providers, so the shell has the same
// way to see what is connected as the session does.
func TestConnectCLIWithoutAProviderListsThem(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant")
	t.Setenv("OPENAI_API_KEY", "")
	var out bytes.Buffer

	err := connectCLI(nil, &out)

	if err == nil || !strings.Contains(err.Error(), "elencode connect <provider>") {
		t.Errorf("err = %v, want it to say how to name one", err)
	}
	for _, want := range []string{"anthropic", "connected · API key from $ANTHROPIC_API_KEY", "openai", "not connected", "chatgpt"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output %q does not mention %q", out.String(), want)
		}
	}
}

// A session holds the credentials it loaded at startup, and another elencode
// may have connected a provider since. Saving must change the file as it is
// now, not write the session's old copy over it.
func TestConnectKeyKeepsWhatAnotherSessionSaved(t *testing.T) {
	cfg := connectionConfig(t, config.Credentials{})
	if err := (config.Credentials{agent.ProviderOpenAI: {APIKey: "sk-oai"}}).Save(cfg.CredentialsPath); err != nil {
		t.Fatal(err)
	}

	if _, _, err := connectKey(context.Background(), cfg, agent.ProviderAnthropic, "sk-ant", (&fakeCheck{}).check); err != nil {
		t.Fatalf("connectKey: %v", err)
	}

	if savedCredentials(t, cfg)[agent.ProviderOpenAI].APIKey != "sk-oai" {
		t.Error("connecting anthropic dropped the key another session saved")
	}
}

func TestDisconnectKeyKeepsWhatAnotherSessionSaved(t *testing.T) {
	cfg := connectionConfig(t, config.Credentials{agent.ProviderAnthropic: {APIKey: "sk-ant"}})
	if err := (config.Credentials{
		agent.ProviderAnthropic: {APIKey: "sk-ant"},
		agent.ProviderOpenAI:    {APIKey: "sk-oai"},
	}).Save(cfg.CredentialsPath); err != nil {
		t.Fatal(err)
	}

	if _, _, err := disconnectProvider(cfg, agent.ProviderAnthropic); err != nil {
		t.Fatalf("disconnectProvider: %v", err)
	}

	if savedCredentials(t, cfg)[agent.ProviderOpenAI].APIKey != "sk-oai" {
		t.Error("disconnecting anthropic dropped the key another session saved")
	}
}

// A key another session already disconnected is gone from the file but not
// from this session's copy, which must let go of it too.
func TestDisconnectKeyForgetsAKeyAlreadyGoneFromTheFile(t *testing.T) {
	cfg := connectionConfig(t, config.Credentials{agent.ProviderOpenAI: {APIKey: "sk-oai"}})
	if err := (config.Credentials{}).Save(cfg.CredentialsPath); err != nil {
		t.Fatal(err)
	}

	disconnected, _, err := disconnectProvider(cfg, agent.ProviderOpenAI)
	if err != nil {
		t.Fatalf("disconnectProvider: %v", err)
	}
	if key, _ := disconnected.APIKey(agent.ProviderOpenAI); key != "" {
		t.Error("the returned config still holds the key")
	}
}

// ChatGPT is disconnected the way a key provider is: its login leaves the file,
// and the keys beside it stay.
func TestDisconnectProviderForgetsTheChatGPTLogin(t *testing.T) {
	cfg := connectionConfig(t, config.Credentials{
		agent.ProviderChatGPT: {Login: &chatgpt.Tokens{AccessToken: "a", RefreshToken: "r", AccountID: "acct"}},
		agent.ProviderOpenAI:  {APIKey: "sk-oai"},
	})

	disconnected, said, err := disconnectProvider(cfg, agent.ProviderChatGPT)
	if err != nil {
		t.Fatalf("disconnectProvider: %v", err)
	}
	saved := savedCredentials(t, cfg)
	if _, ok := saved[agent.ProviderChatGPT]; ok {
		t.Error("the login is still saved")
	}
	if saved[agent.ProviderOpenAI].APIKey != "sk-oai" {
		t.Error("a key went with the login")
	}
	if disconnected.Credentials[agent.ProviderChatGPT].Login != nil {
		t.Error("the returned config still holds the login")
	}
	if !strings.Contains(said, "Disconnected chatgpt") {
		t.Errorf("said %q", said)
	}
}

// On a terminal the key is read without echo, and asked for again after a
// rejection or an empty line, as a typo is the likeliest cause of either.
func TestConnectCLIOnATerminalAsksAgainAfterARejection(t *testing.T) {
	cfg := connectionConfig(t, nil)
	check := &fakeCheck{answers: map[string]error{"sk-typo": errRejected}}
	typed := []string{"sk-typo", "", "sk-good"}
	readSecret := func() (string, error) {
		key := typed[0]
		typed = typed[1:]
		return key, nil
	}
	var out bytes.Buffer

	err := runConnectKey(context.Background(), cfg, agent.ProviderAnthropic, strings.NewReader(""), readSecret, &out, check.check)
	if err != nil {
		t.Fatalf("runConnectKey: %v", err)
	}
	if got := strings.Join(check.asked, ","); got != "sk-typo,sk-good" {
		t.Errorf("checked %s, want the rejected key and then the next one", got)
	}
	if savedCredentials(t, cfg)[agent.ProviderAnthropic].APIKey != "sk-good" {
		t.Error("the accepted key was not saved")
	}
	for _, want := range []string{"Paste your Anthropic API key", "console.anthropic.com", "rejected", "Connected anthropic"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output does not mention %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "sk-good") || strings.Contains(out.String(), "sk-typo") {
		t.Error("the output shows a key")
	}
}

// Giving up at the prompt is giving up, not a failure to explain.
func TestConnectCLIOnATerminalCanBeCancelled(t *testing.T) {
	cfg := connectionConfig(t, nil)
	readSecret := func() (string, error) { return "", errConnectCancelled }

	err := runConnectKey(context.Background(), cfg, agent.ProviderAnthropic, strings.NewReader(""), readSecret, &bytes.Buffer{}, (&fakeCheck{}).check)
	if !errors.Is(err, errConnectCancelled) {
		t.Errorf("err = %v, want it cancelled", err)
	}
}

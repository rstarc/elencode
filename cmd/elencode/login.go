package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rstarc/elencode/internal/agent"
	"github.com/rstarc/elencode/internal/chatgpt"
	"github.com/rstarc/elencode/internal/config"
	"github.com/rstarc/elencode/internal/provider/openai"
)

// runLogin is `elencode connect chatgpt` once its arguments are settled: it
// tells the user what to do, and once they have, what they signed in to and
// where the login went. keys is the terminal to read keys from. With one, the
// login runs in the panel the session shows; without, as when piped, the
// prompts are printed as plain text.
func runLogin(ctx context.Context, s signIn, out io.Writer, keys io.Reader) error {
	var tokens chatgpt.Tokens
	var err error
	if keys != nil {
		tokens, err = runLoginProgram(ctx, s, keys, out, systemClipboard())
	} else {
		tokens, err = s.run(ctx, func(prompt loginPrompt) {
			_, _ = fmt.Fprintf(out, "%s\n\n", prompt.render(false))
			if prompt.page != "" {
				_, _ = fmt.Fprintf(out, "Waiting for you to sign in (ctrl+c to cancel)...\n\n")
			}
		})
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("login cancelled")
	}
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Signed in to ChatGPT%s; the login is saved in %s.\n", account(tokens), s.path)
	_, _ = fmt.Fprintf(out, "Pick a model it serves with /model chatgpt/<id>, for example /model %s.\n", openai.ChatGPTDefault().Qualified())
	return nil
}

// runLoginProgram runs the login under a small Bubble Tea program, so the
// keys the session answers while a login waits work here too.
func runLoginProgram(ctx context.Context, s signIn, keys io.Reader, out io.Writer, clip clipboard) (chatgpt.Tokens, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Buffered past the most a login says, as in the session
	events := make(chan loginEvent, 8)
	go func() {
		tokens, err := s.run(ctx, func(prompt loginPrompt) { events <- loginEvent{prompt: prompt} })
		events <- loginEvent{done: true, tokens: tokens, err: err}
	}()

	program := tea.NewProgram(loginProgram{events: events, cancel: cancel, clipboard: clip}, tea.WithInput(keys), tea.WithOutput(out))
	final, err := program.Run()
	if err != nil {
		return chatgpt.Tokens{}, err
	}
	result := final.(loginProgram).result
	return result.tokens, result.err
}

// loginProgram is `elencode connect chatgpt` on a terminal: the prompts printed as the
// session prints them, and the session's login panel under them.
type loginProgram struct {
	events    <-chan loginEvent
	cancel    context.CancelFunc
	clipboard clipboard
	panel     loginPanel
	finished  bool
	result    loginDoneMsg
}

func (p loginProgram) Init() tea.Cmd {
	return waitForLogin(p.events)
}

func (p loginProgram) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case loginPromptMsg:
		p.panel = p.panel.show(msg.prompt)
		return p, tea.Sequence(tea.Println(msg.prompt.render(true)+"\n"), waitForLogin(msg.events))
	case loginDoneMsg:
		p.finished = true
		p.result = msg
		return p, tea.Quit
	case copiedMsg:
		var cmd tea.Cmd
		p.panel, cmd = p.panel.done(msg)
		return p, cmd
	case copiedExpiredMsg:
		p.panel = p.panel.expire(msg)
		return p, nil
	case tea.KeyPressMsg:
		panel, cmd, cancel := p.panel.press(msg, p.clipboard)
		p.panel = panel
		// The login ends on its own once cancelled, and quits the program then
		if cancel {
			p.cancel()
		}
		return p, cmd
	}
	return p, nil
}

func (p loginProgram) View() tea.View {
	if p.finished {
		return tea.NewView("")
	}
	return tea.NewView(p.panel.view())
}

// account names who a login is for, as a phrase to follow "Signed in to
// ChatGPT", or nothing when the id token does not say.
func account(tokens chatgpt.Tokens) string {
	email := tokens.Email()
	if email == "" {
		return ""
	}
	if plan := tokens.Plan(); plan != "" {
		return " as " + email + " (" + plan + " plan)"
	}
	return " as " + email
}

// loginPrompt is something a login asks of the user. The page is kept apart
// from the words so each side can show it its own way: a link where the
// terminal can follow one, plain text where it cannot.
type loginPrompt struct {
	text string // what to do
	page string // the page to do it on, if any
	code string // the code to enter there, for a device login
}

// render lays the prompt out with the page and the code on lines of their
// own, so either can be copied whole. links makes the page a link, for a
// terminal: the session always is one, `elencode connect` is unless piped, and
// both show a prompt through here.
func (p loginPrompt) render(links bool) string {
	lines := []string{p.text}
	if p.page != "" {
		page := p.page
		if links {
			page = hyperlink(page)
		}
		lines = append(lines, "", "  "+page)
	}
	if p.code != "" {
		lines = append(lines, "", "  "+p.code)
	}
	return strings.Join(lines, "\n")
}

// hyperlink makes page an underlined OSC 8 link. The page is still the text,
// so a terminal that does not follow such links can still find it as a URL,
// or show it to be copied. Underlined as a whole rather than with lipgloss,
// which styles each character on its own.
func hyperlink(page string) string {
	return ansi.SetHyperlink(page) + ansi.NewStyle().Underline(true).Styled(page) + ansi.ResetHyperlink()
}

// isTerminal reports whether out is a terminal, where escape codes are links
// rather than noise.
func isTerminal(out io.Writer) bool {
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// signIn is how a ChatGPT login reaches the user on this machine. Shared by
// `elencode connect chatgpt` and /connect chatgpt, so both sign in the same way; fields so tests
// can stand in for the issuer, the port, the file and the browser.
type signIn struct {
	oauth        chatgpt.OAuth
	callbackAddr string // where the browser login listens for the redirect
	path         string // the credentials file the login is saved in
	// headless means there is no browser here to open, so the user signs in on
	// another device with a code instead.
	headless bool
	// device asks for a code outright, as --device does: for when headless
	// guessed wrong. Unlike a guess it does not fall back to the browser.
	device      bool
	openBrowser func(url string) error
}

// defaultSignIn signs in against the real issuer, from this machine.
func defaultSignIn(path string) signIn {
	return signIn{
		callbackAddr: chatgpt.CallbackAddr,
		path:         path,
		headless:     headless(os.Getenv, runtime.GOOS),
		openBrowser:  openBrowser,
	}
}

// run signs in, saves the login and returns it. say is handed each thing the
// user has to do.
//
// The browser is opened by itself, and the page is shown as well in case it
// did not come up. Without a browser here, the user
// enters a code on any other device; an account that has device codes turned
// off falls back to the browser login.
func (s signIn) run(ctx context.Context, say func(loginPrompt)) (chatgpt.Tokens, error) {
	tokens, err := s.login(ctx, say)
	if err != nil {
		return chatgpt.Tokens{}, err
	}
	if _, err := config.SaveCredential(s.path, agent.ProviderChatGPT, config.Credential{Login: &tokens}); err != nil {
		return chatgpt.Tokens{}, fmt.Errorf("saving the login to %s: %w", s.path, err)
	}
	return tokens, nil
}

func (s signIn) login(ctx context.Context, say func(loginPrompt)) (chatgpt.Tokens, error) {
	if !s.headless && !s.device {
		tokens, err := s.oauth.Login(ctx, s.callbackAddr, func(page string) {
			if err := s.openBrowser(page); err != nil {
				say(openByHand(page))
				return
			}
			say(loginPrompt{text: "Opened your browser to sign in to ChatGPT. If it did not open, open this page:", page: page})
		})
		if errors.Is(err, syscall.EADDRINUSE) {
			return tokens, fmt.Errorf("%w: another login may be waiting on it, or sign in with --device, which needs no port", err)
		}
		return tokens, err
	}

	tokens, err := s.oauth.DeviceLogin(ctx, func(page, code string) {
		say(loginPrompt{
			text: "To sign in to ChatGPT, open this page on any device and enter the code below.\n" +
				"It expires in 15 minutes. Only enter it if you started this login yourself.",
			page: page,
			code: code,
		})
	})
	if !errors.Is(err, chatgpt.ErrDeviceCodeUnavailable) {
		return tokens, err
	}
	if s.device {
		return tokens, fmt.Errorf("%w: sign in without --device to use the browser instead", err)
	}
	say(loginPrompt{text: "Signing in with a device code is not enabled for this ChatGPT account, so this uses the browser login instead.\n" +
		"The browser is sent back to port 1455 on this machine: over SSH, forward it first with ssh -L 1455:127.0.0.1:1455."})
	return s.oauth.Login(ctx, s.callbackAddr, func(page string) { say(openByHand(page)) })
}

func openByHand(page string) loginPrompt {
	return loginPrompt{text: "Open this page in your browser to sign in to ChatGPT:", page: page}
}

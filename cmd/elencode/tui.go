package main

import (
	"context"
	"log"

	"charm.land/bubbles/v2/cursor"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rstarc/elencode/internal/agent"
)

type uiState int

const (
	uiStateIdle       uiState = iota // Agent not active
	uiStateProcessing                // Waiting for Response
)

type model struct {
	// agent state
	agent *agent.Agent
	// TUI state
	viewport viewport.Model
	input    textinput.Model
	state    uiState
	err      error
}

func newModel(agent *agent.Agent) model {

	input := textinput.New()
	input.Placeholder = "start typing..."
	input.SetVirtualCursor(false)
	input.Focus()
	input.Prompt = "> "
	input.CharLimit = 0

	viewport := viewport.New()
	viewport.SetContent("== elencode ==")

	// Only up down scrolling
	viewport.KeyMap.Left.SetEnabled(false)
	viewport.KeyMap.Right.SetEnabled(false)

	return model{
		agent:    agent,
		viewport: viewport,
		input:    input,
		state:    uiStateIdle,
	}
}

type agentResponseMsg struct {
	response agent.Response
	err      error
}

type toolsResultMsg struct {
	results []agent.Block
}

// Command: run the API call
func (m model) processMessageCmd() tea.Cmd {
	a := m.agent
	return func() tea.Msg {
		// TODO: Correctly pass context
		resp, err := a.ProcessTurn(context.Background())
		return agentResponseMsg{resp, err}
	}
}

func (m model) useToolsCmd(response agent.Response) tea.Cmd {
	a := m.agent
	return func() tea.Msg {
		var toolResults []agent.Block
		for _, block := range response.Message.Content {
			if toolUseBlock, ok := block.(agent.ToolUseBlock); ok {
				// TODO: Correctly pass context
				result, err := a.UseTool(context.Background(), toolUseBlock.Name, toolUseBlock.Input)
				toolResults = append(toolResults, agent.NewToolResultBlock(toolUseBlock.ID, result, err != nil))
			}
		}
		return toolsResultMsg{toolResults}
	}
}

// Init implements the bubbletea Model interface
func (m model) Init() tea.Cmd {
	return textinput.Blink
}

// Update implements the bubbletea Model interface
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// TODO: Resize window
		m.viewport.SetWidth(msg.Width)
		m.input.SetWidth(msg.Width)
		// TODO: correctly compute height of text input
		m.viewport.SetHeight(msg.Height - 1)
		m.viewport.GotoBottom()
		return m, nil
	case tea.KeyPressMsg:
		// handle user input
		switch msg.String() {
		case "ctrl+c":
			lipgloss.Println("goodbye")
			return m, tea.Quit
		case "enter":
			// only actually do anything if we are not currently waiting and there is actual input
			if m.state == uiStateIdle && m.input.Value() != "" {
				// Get the text and clear the input
				userInput := m.input.Value()
				m.input.Reset()
				// Update state and send cmd to process user message
				m.state = uiStateProcessing
				// Add user message to context
				userMessage := agent.NewUserMessage([]agent.Block{agent.TextBlock{Text: userInput}})
				m.agent.AppendMessage(userMessage)
				// Update viewport content
				m.viewport.SetContent(agent.RenderTranscript(m.agent))
				m.viewport.GotoBottom()
				// Cmd to process message
				return m, m.processMessageCmd()
			}
		default:
			// Send other keypresses to text input model
			// TODO: Forward to viewport as well
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
	case agentResponseMsg:
		// handle agent response
		if msg.err != nil {
			log.Fatal(msg.err)
		}

		// Add response to context
		m.agent.AppendMessage(msg.response.Message)
		// Update viewport content
		m.viewport.SetContent(agent.RenderTranscript(m.agent))
		m.viewport.GotoBottom()

		if msg.response.StopReason == agent.StopReasonToolUse {
			// evaluate tool use
			return m, m.useToolsCmd(msg.response)
		}

		// no tool use -> return to idle state
		m.state = uiStateIdle
		return m, nil
	case toolsResultMsg:
		// handle finished tool execution
		// Add tool results as user message
		m.agent.AppendMessage(agent.NewUserMessage(msg.results))
		// TODO: Update viewport content
		// Send Cmd to process Message again
		return m, m.processMessageCmd()

	case cursor.BlinkMsg:
		// Forward to textinput
		m.input, _ = m.input.Update(msg)
		return m, nil
	default:
	}

	return m, textinput.Blink
}

// View implements the bubbletea Model interface
func (m model) View() tea.View {
	// TODO: Add throbber/spinner during processing state
	viewportView := m.viewport.View()
	inputView := m.input.View()

	cursor := m.input.Cursor()
	// assemble view
	view := tea.NewView(lipgloss.JoinVertical(lipgloss.Top, viewportView, inputView))
	view.AltScreen = false
	view.Cursor = cursor
	return view
}

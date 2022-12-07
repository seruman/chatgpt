package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wrap"
	"github.com/seruman/chatgpt/chatgpt"
)

func main() {
	ctx := context.Background()
	prompt := chatgpt.NewPrompt(
		chatgpt.NewClient(
			os.Getenv("CHATGPT_SESSION_TOKEN"),
		),
	)

	ch := make(chan string)

	model := initialModel(ch)
	p := tea.NewProgram(model)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case s, ok := <-ch:
				if !ok {
					return
				}

				response, err := prompt.Next(ctx, s)
				if err != nil {
					p.Send(gptMessage{Error: err})
				}

				p.Send(gptMessage{Message: response})
			}
		}
	}()

	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}

type gptMessage struct {
	Message string
	Error   error
}

type (
	errMsg error
)

type model struct {
	messages    []string
	textarea    textarea.Model
	senderStyle lipgloss.Style
	gptStyle    lipgloss.Style
	errorStyle  lipgloss.Style
	err         error

	spinner       spinner.Model
	gptch         chan string
	gptinprogress bool
}

func initialModel(gptch chan string) *model {
	ta := textarea.New()
	ta.Placeholder = "Send a message..."
	ta.Focus()

	ta.Prompt = "┃ "

	ta.SetHeight(3)
	ta.KeyMap.InsertNewline.SetEnabled(false)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()

	ta.ShowLineNumbers = false

	spinner := spinner.New()
	spinner.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))

	return &model{
		gptch:       gptch,
		textarea:    ta,
		messages:    []string{},
		senderStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
		gptStyle:    lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		errorStyle:  lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		spinner:     spinner,
		err:         nil,
	}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spinner.Tick)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var tiCmd tea.Cmd

	m.textarea, tiCmd = m.textarea.Update(msg)
	cmds := []tea.Cmd{tiCmd}

	switch msg := msg.(type) {
	case spinner.TickMsg:
		var sCmd tea.Cmd
		m.spinner, sCmd = m.spinner.Update(msg)
		cmds = append(cmds, sCmd)
	case gptMessage:
		m.gptinprogress = false
		if msg.Error != nil {
			m.messages = append(
				m.messages,
				wrap.String(
					m.gptStyle.Render("GPT:")+" "+m.errorStyle.Render("Error:")+" "+msg.Error.Error(),
					m.textarea.Width(),
				))
		}

		if msg.Message != "" {
			m.messages = append(
				m.messages,
				wrap.String(
					m.gptStyle.Render("GPT:")+" "+msg.Message,
					m.textarea.Width(),
				))
		}
	case tea.WindowSizeMsg:
		m.textarea.SetWidth(msg.Width)
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			fmt.Println(m.textarea.Value())
			return m, tea.Quit
		case tea.KeyEnter:
			if m.gptinprogress {
				break
			}

			text := strings.TrimSpace(m.textarea.Value())

			if text == "" {
				break
			}

			m.messages = append(
				m.messages,
				wrap.String(m.senderStyle.Render("You: ")+text, m.textarea.Width()),
			)
			m.textarea.Reset()

			m.gptinprogress = true
			go func(p string) {
				m.gptch <- p
			}(text)

		}

	case errMsg:
		m.err = msg
		return m, nil
	}

	return m, tea.Batch(cmds...)
}

func (m *model) View() string {
	if m.err != nil {
		return m.err.Error()
	}

	v := strings.Join(m.messages, "\n")

	if m.gptinprogress {
		v += "\n\n" + m.spinner.View()
	}

	v += "\n\n" + m.textarea.View()

	return v + "\n\n"
}

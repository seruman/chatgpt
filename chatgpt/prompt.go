package chatgpt

import (
	"context"

	"github.com/google/uuid"
)

type Prompt struct {
	client *Client

	current  *State
	previous *State
}

type State struct {
	ConversationID  string
	ParentMessageID string
}

func NewPrompt(client *Client) *Prompt {
	return &Prompt{
		client: client,
		current: &State{
			ConversationID:  "",
			ParentMessageID: uuid.NewString(),
		},
	}
}

func (p *Prompt) Next(ctx context.Context, prompt string) (string, error) {
	var message string
	handler := func(resp ConversationResponse, err error) {
		if err == nil {
			if len(resp.Message.Content.Parts) > 0 {
				message = resp.Message.Content.Parts[0]
			}
		}
	}

	err := p.streamNext(ctx, prompt, handler)
	if err != nil {
		return "", err
	}

	return message, nil
}

func (p *Prompt) streamNext(ctx context.Context, prompt string, handler ConversationStreamHandler) error {
	state := *p.current
	var streamErr error
	err := p.client.Conversation(ctx, ConversationRequest{
		Action: ActionNext,
		Messages: []Message{
			{
				ID:   uuid.NewString(),
				Role: RoleUser,
				Content: Content{
					ContentType: ContentTypeText,
					Parts:       []string{prompt},
				},
			},
		},
		ParentMessageID: state.ParentMessageID,
		ConversationID:  state.ConversationID,
		Model:           ModelTextDavinci002Render,
	},
		func(resp ConversationResponse, err error) {
			streamErr = err
			handler(resp, err)

			if err == nil {
				state = State{
					ConversationID:  resp.ConversationID,
					ParentMessageID: resp.Message.ID,
				}
			}
		},
	)
	if err != nil {
		return err
	}

	if streamErr != nil {
		return streamErr
	}

	p.previous = p.current
	p.current = &state

	return nil
}

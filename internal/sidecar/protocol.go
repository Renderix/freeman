package sidecar

import (
	"encoding/json"
	"fmt"
	"io"
)

// Message type constants (sent over JSONL in both directions).
const (
	// Freeman → Sidecar
	MsgTypeStart        = "start"
	MsgTypeAskUserReply = "ask_user_reply"
	MsgTypeCancel       = "cancel"

	// Sidecar → Freeman
	MsgTypeAssistantText = "assistant_text"
	MsgTypeAskUser       = "ask_user"
	MsgTypeDone          = "done"
	MsgTypeError         = "error"
)

// Message is the common interface for any protocol message.
type Message interface{ isMessage() }

// ObjectivePayload is the serialized form of a call.Objective sent to the sidecar.
type ObjectivePayload struct {
	Goal               string   `json:"goal"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	Constraints        []string `json:"constraints"`
	Notes              []string `json:"notes"`
	Model              string   `json:"model"` // "sonnet" or "opus"
}

// StartMsg kicks off a sidecar session.
type StartMsg struct {
	Type      string           `json:"type"`
	Objective ObjectivePayload `json:"objective"`
}

func (StartMsg) isMessage() {}

// AskUserReplyMsg is Freeman's answer to a previous AskUser from the sidecar.
type AskUserReplyMsg struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Answer string `json:"answer"`
}

func (AskUserReplyMsg) isMessage() {}

// CancelMsg aborts the sidecar session.
type CancelMsg struct {
	Type string `json:"type"`
}

func (CancelMsg) isMessage() {}

// AssistantTextMsg is streamed intermediate text from Claude.
type AssistantTextMsg struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (AssistantTextMsg) isMessage() {}

// AskUserMsg is the sidecar asking Freeman a question via the ask_user tool.
type AskUserMsg struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Question string `json:"question"`
}

func (AskUserMsg) isMessage() {}

// DoneMsg is clean completion.
type DoneMsg struct {
	Type    string `json:"type"`
	Summary string `json:"summary"`
}

func (DoneMsg) isMessage() {}

// ErrorMsg is an error from the sidecar.
type ErrorMsg struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (ErrorMsg) isMessage() {}

// Encode writes a single message as a JSONL line to w.
func Encode(w io.Writer, m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, err = w.Write([]byte("\n"))
	return err
}

// Decode parses a single JSONL line into a typed message.
func Decode(line []byte) (Message, error) {
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	switch head.Type {
	case MsgTypeStart:
		var m StartMsg
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, err
		}
		return m, nil
	case MsgTypeAskUserReply:
		var m AskUserReplyMsg
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, err
		}
		return m, nil
	case MsgTypeCancel:
		var m CancelMsg
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, err
		}
		return m, nil
	case MsgTypeAssistantText:
		var m AssistantTextMsg
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, err
		}
		return m, nil
	case MsgTypeAskUser:
		var m AskUserMsg
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, err
		}
		return m, nil
	case MsgTypeDone:
		var m DoneMsg
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, err
		}
		return m, nil
	case MsgTypeError:
		var m ErrorMsg
		if err := json.Unmarshal(line, &m); err != nil {
			return nil, err
		}
		return m, nil
	default:
		return nil, fmt.Errorf("unknown message type: %q", head.Type)
	}
}

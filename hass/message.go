package hass

import (
	"fmt"
	"time"

	"github.com/goccy/go-json"
)

const (
	MessageTypeResult       = "result"
	MessageTypeAuthRequired = "auth_required"
	MessageTypeAuthOK       = "auth_ok"
	MessageTypeAuthInvalid  = "auth_invalid"
	MessageTypeEvent        = "event"

	MessageTypeAuth            = "auth"
	MessageTypeSubscribeEvents = "subscribe_events"
)

type BaseMessage struct {
	ID   int    `json:"id,omitempty"`
	Type string `json:"type"`
}

type ResultMessage struct {
	BaseMessage
	Success bool               `json:"success"`
	Error   ResultMessageError `json:"error,omitempty"`
}

type ResultMessageError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// AuthMessage is a message sent to Home Assistant to authenticate.
// Type is "auth".
type AuthMessage struct {
	BaseMessage
	AccessToken string `json:"access_token"`
}

// AuthRequiredMessage is a message sent by Home Assistant when authentication is required.
// Type is "auth_required".
type AuthRequiredMessage struct {
	BaseMessage
	Version string `json:"ha_version"`
}

type AuthOKMessage struct {
	BaseMessage
	Version string `json:"ha_version"`
}

type AuthInvalidMessage struct {
	BaseMessage
	Message string `json:"message"`
}

type SubscribeEventsMessage struct {
	BaseMessage
	EventType EventType `json:"event_type"`
}

type EventMessage struct {
	BaseMessage
	Event Event `json:"event"`
}

type EventType string

const (
	EventTypeStateChanged EventType = "state_changed"
)

type EventData struct {
	EntityID string          `json:"entity_id"`
	OldState json.RawMessage `json:"old_state"`
	NewState json.RawMessage `json:"new_state"`
}

type EventContext struct {
	ID       string  `json:"id"`
	ParentID *string `json:"parent_id"`
	UserID   *string `json:"user_id"`
}

type Event struct {
	EventType EventType    `json:"event_type"`
	TimeFired time.Time    `json:"time_fired"`
	Origin    string       `json:"origin"`
	Context   EventContext `json:"context"`
	Data      EventData    `json:"data"`
}

func UnmarshalMessage(raw json.RawMessage) (any, error) {
	var m BaseMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}

	switch m.Type {
	case MessageTypeResult:
		var m ResultMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		return m, nil
	case MessageTypeAuthRequired:
		var m AuthRequiredMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		return m, nil
	case MessageTypeAuthOK:
		var m AuthOKMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		return m, nil
	case MessageTypeAuthInvalid:
		var m AuthInvalidMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		return m, nil
	case MessageTypeEvent:
		var m EventMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		return m, nil
	default:
		return nil, fmt.Errorf("unknown raw type: %s", m.Type)
	}
}

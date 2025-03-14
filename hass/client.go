package hass

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/goccy/go-json"
	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
)

// Client is a websocket API client for Home Assistant
type Client struct {
	Host  string
	Token string

	receiveCtx         context.Context
	receiveCancel      context.CancelFunc
	activeReceiversNum int
	activeReceivers    map[int]chan interface{}
	activeReceiversMtx sync.Mutex

	conn                         *websocket.Conn
	isAuthenticated              bool
	receiverBufferSize           int
	subscribeEventsResultTimeout time.Duration
}

func (c *Client) WaitAuthenticated(ctx context.Context) error {
	for !c.isAuthenticated {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	return nil
}

func (c *Client) Connect(ctx context.Context) error {
	url := fmt.Sprintf("%s/api/websocket", c.Host)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, http.Header{ //nolint:bodyclose
		"User-Agent": []string{"hass2ch"},
	})

	if err != nil {
		return err
	}

	c.conn = conn
	c.receiveCtx, c.receiveCancel = context.WithCancel(context.Background())

	go c.receive()

	return nil
}

const (
	subscribeEventsResultDefaultTimeout = time.Second * 5
)

type SubscribeEventsOption func(message *SubscribeEventsMessage)

func SubscribeEventsWithEventType(eventType EventType) SubscribeEventsOption {
	return func(message *SubscribeEventsMessage) {
		message.EventType = eventType
	}
}

// SubscribeEvents subscribes to Home Assistant events and returns a channel that will receive the events
//
//nolint:gocyclo
func (c *Client) SubscribeEvents(ctx context.Context, opts ...SubscribeEventsOption) (chan *EventMessage, error) {
	c.activeReceiversMtx.Lock()
	c.activeReceiversNum++
	receiverNum := c.activeReceiversNum

	// Create subscription command
	cmd := SubscribeEventsMessage{
		BaseMessage: BaseMessage{
			ID:   receiverNum,
			Type: MessageTypeSubscribeEvents,
		},
	}

	for _, opt := range opts {
		opt(&cmd)
	}

	payload, err := json.Marshal(cmd)
	if err != nil {
		c.activeReceiversMtx.Unlock()
		return nil, fmt.Errorf("failed to marshal subscribe events message: %w", err)
	}

	if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		c.activeReceiversMtx.Unlock()
		return nil, fmt.Errorf("failed to send message to Home Assistant: %w", err)
	}

	if c.activeReceivers == nil {
		bufferSize := 1
		if c.receiverBufferSize > 0 {
			bufferSize = c.receiverBufferSize
		}
		c.activeReceivers = make(map[int]chan interface{}, bufferSize)
	}

	// Create a channel to receive all message types
	resultChan := make(chan interface{})
	c.activeReceivers[receiverNum] = resultChan
	c.activeReceiversMtx.Unlock()

	// Create a timeout context for waiting for the subscription result
	resultTimeout := subscribeEventsResultDefaultTimeout
	if c.subscribeEventsResultTimeout > 0 {
		resultTimeout = c.subscribeEventsResultTimeout
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, resultTimeout)
	defer cancel()

	// Wait for the subscription result
	select {
	case <-timeoutCtx.Done():
		c.closeReceiver(receiverNum)
		return nil, fmt.Errorf("timeout waiting for Home Assistant to acknowledge subscription")
	case msg := <-resultChan:
		// Check if the message is a result message
		result, ok := msg.(ResultMessage)
		if !ok {
			c.closeReceiver(receiverNum)
			log.Error().Interface("message", msg).Msg("Unexpected message type received waiting for a result")
			return nil, fmt.Errorf("unexpected message type received waiting for a result")
		}

		// Check if the subscription was successful
		if !result.Success {
			c.closeReceiver(receiverNum)
			log.Error().
				Str("code", result.Error.Code).
				Str("message", result.Error.Message).
				Msg("Subscription failed")

			return nil, fmt.Errorf("subscription failed: %s: %s", result.Error.Code, result.Error.Message)
		}

		log.Info().Int("id", receiverNum).Msg("Subscribed to events")
	}

	// Create a channel for events only
	eventChan := make(chan *EventMessage)

	// Start a goroutine to forward events to the event channel
	go func() {
		defer close(eventChan)
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-resultChan:
				if !ok {
					return
				}

				// Only forward event messages
				if eventMsg, ok := msg.(*EventMessage); ok {
					eventChan <- eventMsg
				}
			}
		}
	}()

	return eventChan, nil
}

// GetStates gets all states from Home Assistant
func (c *Client) GetStates(ctx context.Context) ([]State, error) {
	c.activeReceiversMtx.Lock()
	c.activeReceiversNum++
	receiverNum := c.activeReceiversNum

	// Create get states command
	cmd := BaseMessage{
		ID:   receiverNum,
		Type: "get_states",
	}

	payload, err := json.Marshal(cmd)
	if err != nil {
		c.activeReceiversMtx.Unlock()
		return nil, fmt.Errorf("failed to marshal get states message: %w", err)
	}

	if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		c.activeReceiversMtx.Unlock()
		return nil, fmt.Errorf("failed to send message to Home Assistant: %w", err)
	}

	if c.activeReceivers == nil {
		bufferSize := 1
		if c.receiverBufferSize > 0 {
			bufferSize = c.receiverBufferSize
		}
		c.activeReceivers = make(map[int]chan interface{}, bufferSize)
	}

	// Create a channel to receive the result
	resultChan := make(chan interface{})
	c.activeReceivers[receiverNum] = resultChan
	c.activeReceiversMtx.Unlock()

	// Create a timeout context for waiting for the get states result
	resultTimeout := subscribeEventsResultDefaultTimeout
	if c.subscribeEventsResultTimeout > 0 {
		resultTimeout = c.subscribeEventsResultTimeout
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, resultTimeout)
	defer cancel()

	// Wait for the get states result
	select {
	case <-timeoutCtx.Done():
		c.closeReceiver(receiverNum)
		return nil, fmt.Errorf("timeout waiting for Home Assistant to acknowledge get states")
	case msg := <-resultChan:
		// Check if the message is a result message
		result, ok := msg.(ResultMessage)
		if !ok {
			c.closeReceiver(receiverNum)
			log.Error().Interface("message", msg).Msg("Unexpected message type received waiting for a result")
			return nil, fmt.Errorf("unexpected message type received waiting for a result")
		}

		// Check if the get states was successful
		if !result.Success {
			c.closeReceiver(receiverNum)
			log.Error().
				Str("code", result.Error.Code).
				Str("message", result.Error.Message).
				Msg("Get states failed")

			return nil, fmt.Errorf("get states failed: %s: %s", result.Error.Code, result.Error.Message)
		}

		log.Info().Int("id", receiverNum).Msg("Received states")

		// Parse the result as a list of states
		var states []State
		if err := json.Unmarshal(result.Result, &states); err != nil {
			return nil, fmt.Errorf("failed to parse states: %w", err)
		}

		return states, nil
	}
}

func (c *Client) closeReceiver(receiverNum int) {
	c.activeReceiversMtx.Lock()
	defer c.activeReceiversMtx.Unlock()
	if ch, ok := c.activeReceivers[receiverNum]; ok {
		close(ch)
		delete(c.activeReceivers, receiverNum)
	}
}

func (c *Client) receive() {
	for {
		select {
		case <-c.receiveCtx.Done():
			log.Debug().Msg("Closing Home Assistant websocket receive message loop")
			return
		default:
			_, payload, err := c.conn.ReadMessage()
			if err != nil {
				if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					log.Info().Msg("Home Assistant websocket connection closed")
					return
				}

				log.Err(err).Msg("Failed to read message from Home Assistant websocket")
				continue
			}

			// Parse the message
			msg, err := UnmarshalMessage(payload)
			if err != nil {
				log.Error().Err(err).Msg("Failed to parse message from Home Assistant")
				continue
			}

			// Handle different message types
			switch m := msg.(type) {
			case AuthRequiredMessage:
				c.authenticate()
			case AuthOKMessage:
				c.isAuthenticated = true
				log.Info().Str("version", m.Version).Msg("Authenticated with Home Assistant")
			case AuthInvalidMessage:
				log.Error().Str("message", m.Message).Msg("Failed to authenticate with Home Assistant")
			case *EventMessage, ResultMessage:
				c.handleMessage(m)
			default:
				log.Debug().Interface("message", msg).Msg("Received unhandled message type from Home Assistant")
			}
		}
	}
}

func (c *Client) handleMessage(msg interface{}) {
	// Get the message ID
	var id int
	switch m := msg.(type) {
	case *EventMessage:
		id = m.ID
	case ResultMessage:
		id = m.ID
	default:
		log.Warn().Interface("message", msg).Msg("Cannot determine ID of message")
		return
	}

	if id == 0 {
		log.Warn().Msg("Received message from Home Assistant without an ID")
		return
	}

	c.activeReceiversMtx.Lock()
	defer c.activeReceiversMtx.Unlock()

	if c.activeReceivers == nil || c.activeReceivers[id] == nil {
		log.Warn().Int("id", id).Interface("message", msg).Msg("Received message from Home Assistant with an unknown ID")
		return
	}

	c.activeReceivers[id] <- msg
}

func (c *Client) Close() error {
	log.Info().Msg("Closing Home Assistant websocket connection")

	if c.receiveCancel != nil {
		c.receiveCancel()
	}

	return c.conn.Close()
}

func (c *Client) authenticate() {
	if c.isAuthenticated {
		log.Warn().Msg("Received auth_required message from Home Assistant while already authenticated")
	}

	log.Info().Msg("Authenticating with Home Assistant")

	// Create authentication message
	authMsg := AuthMessage{
		BaseMessage: BaseMessage{
			Type: MessageTypeAuth,
		},
		AccessToken: c.Token,
	}

	payload, err := json.Marshal(authMsg)
	if err != nil {
		log.Err(err).Msg("Failed to marshal auth message")
		return
	}

	if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		log.Err(err).Msg("Failed to send auth message to Home Assistant")
		return
	}
}

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

	// Reconnection settings
	reconnectMu            sync.Mutex
	isReconnecting         bool
	subscriptions          []subscriptionInfo
	reconnectInterval      time.Duration
	maxReconnectInterval   time.Duration
	reconnectBackoffFactor float64
}

type subscriptionInfo struct {
	ctx        context.Context
	eventType  EventType
	outputChan chan *EventMessage // The channel returned to the caller
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

// WithReconnectConfig sets the reconnection configuration for the client
func WithReconnectConfig(initialInterval, maxInterval time.Duration, backoffFactor float64) func(*Client) {
	return func(c *Client) {
		c.reconnectInterval = initialInterval
		c.maxReconnectInterval = maxInterval
		c.reconnectBackoffFactor = backoffFactor
	}
}

// NewClient creates a new Home Assistant client with the given host and token.
// The client supports automatic reconnection with configurable backoff.
//
// It maintains subscriptions across reconnects, ensuring that event channels
// remain valid even if the underlying WebSocket connection is lost and re-established.
func NewClient(host, token string, opts ...func(*Client)) *Client {
	c := &Client{
		Host:                   host,
		Token:                  token,
		reconnectInterval:      1 * time.Second,
		maxReconnectInterval:   30 * time.Second,
		reconnectBackoffFactor: 1.5,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// Connect establishes a connection to the Home Assistant WebSocket API
func (c *Client) Connect(ctx context.Context) error {
	url := fmt.Sprintf("%s/api/websocket", c.Host)

	log.Info().Str("url", url).Msg("Connecting to Home Assistant")

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, http.Header{ //nolint:bodyclose
		"User-Agent": []string{"hass2ch"},
	})

	if err != nil {
		log.Error().Err(err).Str("url", url).Msg("Failed to connect to Home Assistant")
		return err
	}

	c.conn = conn
	c.isAuthenticated = false
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

// SubscribeEvents subscribes to Home Assistant events and returns a channel that will receive the events.
// The returned channel remains valid across connection failures and reconnections, ensuring uninterrupted
// event delivery. The subscription is automatically restored if the connection is lost and re-established.
//
//nolint:gocyclo
func (c *Client) SubscribeEvents(ctx context.Context, opts ...SubscribeEventsOption) (chan *EventMessage, error) {
	// Create subscription command
	cmd := SubscribeEventsMessage{
		BaseMessage: BaseMessage{
			Type: MessageTypeSubscribeEvents,
		},
	}

	for _, opt := range opts {
		opt(&cmd)
	}

	// Create the stable output channel that will be returned to the caller
	// This channel will persist across reconnections
	outputChan := make(chan *EventMessage, 100) // Buffer to prevent blocking during reconnection

	// Store subscription info for reconnection
	c.reconnectMu.Lock()
	subscription := subscriptionInfo{
		ctx:        ctx,
		eventType:  cmd.EventType,
		outputChan: outputChan,
	}
	c.subscriptions = append(c.subscriptions, subscription)
	c.reconnectMu.Unlock()

	// Start the initial subscription
	if err := c.startSubscription(ctx, cmd.EventType, outputChan); err != nil {
		// Remove this subscription from our list since it failed
		c.reconnectMu.Lock()
		for i, sub := range c.subscriptions {
			if sub.outputChan == outputChan {
				c.subscriptions = append(c.subscriptions[:i], c.subscriptions[i+1:]...)
				break
			}
		}
		c.reconnectMu.Unlock()

		close(outputChan)
		return nil, err
	}

	return outputChan, nil
}

// startSubscription initiates a subscription to Home Assistant events
// and forwards events to the provided output channel
//
//nolint:gocyclo
func (c *Client) startSubscription(ctx context.Context, eventType EventType, outputChan chan *EventMessage) error {
	c.activeReceiversMtx.Lock()
	c.activeReceiversNum++
	receiverNum := c.activeReceiversNum

	// Create subscription command
	cmd := SubscribeEventsMessage{
		BaseMessage: BaseMessage{
			ID:   receiverNum,
			Type: MessageTypeSubscribeEvents,
		},
		EventType: eventType,
	}

	payload, err := json.Marshal(cmd)
	if err != nil {
		c.activeReceiversMtx.Unlock()
		return fmt.Errorf("failed to marshal subscribe events message: %w", err)
	}

	if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
		c.activeReceiversMtx.Unlock()
		return fmt.Errorf("failed to send message to Home Assistant: %w", err)
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
		return fmt.Errorf("timeout waiting for Home Assistant to acknowledge subscription")
	case msg := <-resultChan:
		// Check if the message is a result message
		result, ok := msg.(ResultMessage)
		if !ok {
			c.closeReceiver(receiverNum)
			log.Error().Interface("message", msg).Msg("Unexpected message type received waiting for a result")
			return fmt.Errorf("unexpected message type received waiting for a result")
		}

		// Check if the subscription was successful
		if !result.Success {
			c.closeReceiver(receiverNum)
			log.Error().
				Str("code", result.Error.Code).
				Str("message", result.Error.Message).
				Msg("Subscription failed")

			return fmt.Errorf("subscription failed: %s: %s", result.Error.Code, result.Error.Message)
		}

		log.Info().
			Int("id", receiverNum).
			Str("event_type", string(cmd.EventType)).
			Msg("Subscribed to events")
	}

	// Start a goroutine to forward events to the output channel
	go func() {
		for {
			select {
			case <-ctx.Done():
				c.closeReceiver(receiverNum)
				return
			case msg, ok := <-resultChan:
				if !ok {
					log.Warn().
						Int("id", receiverNum).
						Str("event_type", string(cmd.EventType)).
						Msg("Event channel closed, connection lost")
					return // Will be reconnected by reconnect routine
				}

				// Only forward event messages to the output channel
				if eventMsg, ok := msg.(*EventMessage); ok {
					log.Debug().
						Str("event_type", string(eventMsg.Event.EventType)).
						Str("entity_id", eventMsg.Event.Data.EntityID).
						Msg("Received event")

					select {
					case outputChan <- eventMsg:
						// Successfully sent the event
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return nil
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

// reconnect handles reconnection to Home Assistant with exponential backoff
func (c *Client) reconnect(ctx context.Context) {
	c.reconnectMu.Lock()
	if c.isReconnecting {
		c.reconnectMu.Unlock()
		return
	}
	c.isReconnecting = true
	c.reconnectMu.Unlock()

	go func() {
		defer func() {
			c.reconnectMu.Lock()
			c.isReconnecting = false
			c.reconnectMu.Unlock()
		}()

		// Close the current connection gracefully
		if c.conn != nil {
			_ = c.conn.Close()
		}

		// Make a copy of subscriptions to restore after reconnection
		c.reconnectMu.Lock()
		subscriptions := make([]subscriptionInfo, len(c.subscriptions))
		copy(subscriptions, c.subscriptions)
		c.reconnectMu.Unlock()

		// Attempt to reconnect with exponential backoff
		interval := c.reconnectInterval
		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("Context canceled, stopping reconnection attempts")
				return
			default:
				log.Info().Dur("interval", interval).Msg("Attempting to reconnect to Home Assistant")

				if err := c.Connect(ctx); err != nil {
					log.Error().Err(err).Dur("interval", interval).Msg("Failed to reconnect to Home Assistant")

					// Wait and increase backoff interval
					time.Sleep(interval)
					interval = time.Duration(float64(interval) * c.reconnectBackoffFactor)
					if interval > c.maxReconnectInterval {
						interval = c.maxReconnectInterval
					}
					continue
				}

				// Wait for authentication
				if err := c.WaitAuthenticated(ctx); err != nil {
					log.Error().Err(err).Msg("Failed to authenticate after reconnection")
					continue
				}

				log.Info().Msg("Successfully reconnected to Home Assistant")

				// Restore subscriptions using the same output channels
				for _, sub := range subscriptions {
					log.Info().
						Str("event_type", string(sub.eventType)).
						Msg("Restoring subscription after reconnection")

					if err := c.startSubscription(sub.ctx, sub.eventType, sub.outputChan); err != nil {
						log.Error().
							Err(err).
							Str("event_type", string(sub.eventType)).
							Msg("Failed to restore subscription after reconnection")
					} else {
						log.Info().
							Str("event_type", string(sub.eventType)).
							Msg("Successfully restored subscription after reconnection")
					}
				}

				return
			}
		}
	}()
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
					// Try to reconnect when connection is closed
					c.reconnect(context.Background())
					return
				}

				// For other errors, still attempt to reconnect
				c.reconnect(context.Background())
				return
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

	// Cancel the receive context to stop the message loop
	if c.receiveCancel != nil {
		c.receiveCancel()
	}

	// Close all subscription output channels
	c.reconnectMu.Lock()
	for _, sub := range c.subscriptions {
		close(sub.outputChan)
	}
	c.subscriptions = nil
	c.reconnectMu.Unlock()

	// Close the WebSocket connection
	if c.conn != nil {
		return c.conn.Close()
	}

	return nil
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

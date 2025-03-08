package hass

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fastjson"
)

// Client is a websocket API client for Home Assistant
type Client struct {
	Host  string
	Token string

	receiveCtx         context.Context
	activeReceiversNum int
	activeReceivers    map[int]chan *fastjson.Value
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
		}
	}

	return nil
}

func (c *Client) Connect(ctx context.Context) error {
	url := fmt.Sprintf("%s/api/websocket", c.Host)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, http.Header{
		"User-Agent": []string{"hass2ch"},
	})

	if err != nil {
		return err
	}

	c.conn = conn
	c.receiveCtx = context.Background()

	go c.receive()

	return nil
}

const (
	subscribeEventsCommandPayload       = `{"id": %d, "type": "subscribe_events"}`
	subscribeEventsResultDefaultTimeout = time.Second * 5
)

func (c *Client) SubscribeEvents(ctx context.Context) (chan *fastjson.Value, error) {
	c.activeReceiversMtx.Lock()
	c.activeReceiversNum++
	receiverNum := c.activeReceiversNum

	payload := fmt.Sprintf(subscribeEventsCommandPayload, receiverNum)

	if err := c.conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
		return nil, fmt.Errorf("failed to send message to Home Assistant: %w", err)
	}

	if c.activeReceivers == nil {
		bufferSize := 1
		if c.receiverBufferSize > 0 {
			bufferSize = c.receiverBufferSize
		}
		c.activeReceivers = make(map[int]chan *fastjson.Value, bufferSize)
	}

	resultChan := make(chan *fastjson.Value)
	c.activeReceivers[receiverNum] = resultChan
	c.activeReceiversMtx.Unlock()

	resultTimeout := subscribeEventsResultDefaultTimeout
	if c.subscribeEventsResultTimeout > 0 {
		resultTimeout = c.subscribeEventsResultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, resultTimeout)
	defer cancel()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for Home Assistant to acknowledge subscription")
	case v := <-resultChan:
		if typ := string(v.GetStringBytes("type")); typ != MessageTypeResult {
			log.Error().Str("type", typ).Msg("Unexpected message type received waiting for a result")

			return nil, fmt.Errorf("unexpected message type received waiting for a result: %s", typ)
		}

		success := v.GetBool("success")
		if !success {
			defer c.closeReceiver(receiverNum)

			log.Error().
				Str("code", string(v.GetStringBytes("error.code"))).
				Str("message", string(v.GetStringBytes("error.message"))).
				Msg("Subscription failed")

			return nil, fmt.Errorf(
				"subscription failed: %s: %s",
				string(v.GetStringBytes("error.code")),
				string(v.GetStringBytes("error.message")),
			)
		}

		log.Info().
			Int("id", receiverNum).
			Msg("Subscribed to events")
	}

	return resultChan, nil
}

func (c *Client) GetStates(ctx context.Context) (*fastjson.Value, error) {
	c.activeReceiversMtx.Lock()
	c.activeReceiversNum++
	receiverNum := c.activeReceiversNum

	payload := fmt.Sprintf(`{"id": %d, "type": "get_states"}`, receiverNum)

	if err := c.conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
		return nil, fmt.Errorf("failed to send message to Home Assistant: %w", err)
	}

	if c.activeReceivers == nil {
		bufferSize := 1
		if c.receiverBufferSize > 0 {
			bufferSize = c.receiverBufferSize
		}
		c.activeReceivers = make(map[int]chan *fastjson.Value, bufferSize)
	}

	resultChan := make(chan *fastjson.Value)
	c.activeReceivers[receiverNum] = resultChan
	c.activeReceiversMtx.Unlock()

	resultTimeout := subscribeEventsResultDefaultTimeout
	if c.subscribeEventsResultTimeout > 0 {
		resultTimeout = c.subscribeEventsResultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, resultTimeout)
	defer cancel()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("timeout waiting for Home Assistant to acknowledge subscription")
	case v := <-resultChan:
		if typ := string(v.GetStringBytes("type")); typ != MessageTypeResult {
			log.Error().Str("type", typ).Msg("Unexpected message type received waiting for a result")

			return nil, fmt.Errorf("unexpected message type received waiting for a result: %s", typ)
		}

		success := v.GetBool("success")
		if !success {
			defer c.closeReceiver(receiverNum)

			log.Error().
				Str("code", string(v.GetStringBytes("error.code"))).
				Str("message", string(v.GetStringBytes("error.message"))).
				Msg("Failed to get states")

			return nil, fmt.Errorf(
				"failed to get states: %s: %s",
				string(v.GetStringBytes("error.code")),
				string(v.GetStringBytes("error.message")),
			)
		}

		log.Info().
			Int("id", receiverNum).
			Msg("Received states")

		return v, nil
	}
}

func (c *Client) closeReceiver(receiverNum int) {
	c.activeReceiversMtx.Lock()
	defer c.activeReceiversMtx.Unlock()
	close(c.activeReceivers[receiverNum])
	delete(c.activeReceivers, receiverNum)
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

			v, err := fastjson.ParseBytes(payload)

			typ := string(v.GetStringBytes("type"))

			if typ == "" {
				log.Error().Msg("Received message from Home Assistant without a type")
				continue
			}

			switch typ {
			case MessageTypeAuthRequired:
				c.authenticate()
			case MessageTypeAuthOK:
				c.isAuthenticated = true
				version := string(v.GetStringBytes("ha_version"))
				log.Info().Str("version", version).Msg("Authenticated with Home Assistant")
			case MessageTypeAuthInvalid:
				log.Error().Bytes(
					"message",
					v.GetStringBytes("message"),
				).Msg("Failed to authenticate with Home Assistant")
			default:
				c.handleMessage(v)
			}
		}
	}
}

func (c *Client) handleMessage(v *fastjson.Value) {
	id := v.GetInt("id")

	if id == 0 {
		log.Warn().Msg("Received message from Home Assistant without an ID")
		return
	}

	c.activeReceiversMtx.Lock()
	defer c.activeReceiversMtx.Unlock()

	if c.activeReceivers == nil || c.activeReceivers[id] == nil {
		log.Warn().Int("id", id).Str("message", v.String()).Msg("Received message from Home Assistant with an unknown ID")
		return
	}

	c.activeReceivers[id] <- v
}

func (c *Client) Close() error {
	log.Info().Msg("Closing Home Assistant websocket connection")

	c.receiveCtx.Done()
	return c.conn.Close()
}

func (c *Client) authenticate() {
	if c.isAuthenticated {
		log.Warn().Msg("Received auth_required message from Home Assistant while already authenticated")
	}

	log.Info().Msg("Authenticating with Home Assistant")

	if err := c.conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"type": "auth", "access_token": "%s"}`, c.Token))); err != nil {
		log.Err(err).Msg("Failed to send auth message to Home Assistant")
		return
	}
}

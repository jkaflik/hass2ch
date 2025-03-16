package test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jkaflik/hass2ch/hass"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
)

// This test requires a running Home Assistant instance
// Run the test with:
// HASS_TOKEN=<token> HASS_HOST=localhost:8123 go test -v ./test/...
func TestClientReconnection(t *testing.T) {
	// Configure logging
	log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()
	log.Logger.Level(zerolog.DebugLevel)

	// Skip if not running in CI or explicitly enabled
	host := os.Getenv("HASS_HOST")
	token := os.Getenv("HASS_TOKEN")
	if host == "" || token == "" {
		t.Skip("Skipping test: HASS_HOST or HASS_TOKEN not set")
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create client with fast reconnection settings for testing
	client := hass.NewClient(
		"ws://"+host,
		token,
		hass.WithReconnectConfig(
			1*time.Second, // Initial reconnect interval
			5*time.Second, // Max reconnect interval
			1.5,           // Backoff multiplier
		),
	)

	// Connect to Home Assistant
	err := client.Connect(ctx)
	require.NoError(t, err, "Failed to connect to Home Assistant")

	// Wait for authentication
	err = client.WaitAuthenticated(ctx)
	require.NoError(t, err, "Failed to authenticate with Home Assistant")

	// Subscribe to events
	events, err := client.SubscribeEvents(ctx, hass.SubscribeEventsWithEventType(hass.EventTypeStateChanged))
	require.NoError(t, err, "Failed to subscribe to events")

	// Start a goroutine to receive events
	receivedEvents := make(chan struct{}, 10)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-events:
				if !ok {
					t.Log("Events channel closed")
					return
				}
				receivedEvents <- struct{}{}
			}
		}
	}()

	// Wait for initial events
	select {
	case <-receivedEvents:
		t.Log("Received initial event")
	case <-time.After(30 * time.Second):
		t.Fatal("Timeout waiting for initial events")
	}

	// Close the client to simulate connection loss
	t.Log("Closing client to simulate connection loss")
	err = client.Close()
	require.NoError(t, err, "Failed to close client")

	// Reconnect
	t.Log("Reconnecting client")
	err = client.Connect(ctx)
	require.NoError(t, err, "Failed to reconnect to Home Assistant")

	// Wait for authentication
	err = client.WaitAuthenticated(ctx)
	require.NoError(t, err, "Failed to authenticate after reconnection")

	// Wait for events after reconnection
	select {
	case <-receivedEvents:
		t.Log("Received event after reconnection")
	case <-time.After(30 * time.Second):
		t.Fatal("Timeout waiting for events after reconnection")
	}

	// Cleanup
	err = client.Close()
	require.NoError(t, err, "Failed to close client")
}

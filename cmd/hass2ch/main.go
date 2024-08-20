package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/jkaflik/hass2ch/hass"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	log.Logger.Level(zerolog.DebugLevel)

	c := &hass.Client{
		Host:  "wss://hass.kaflik.tech",
		Token: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJkMDIwZWM0YWVjNTU0ZmMzOGY5NmNkMjhiMTRkZGRkYiIsImlhdCI6MTcyMjg3MzUzNSwiZXhwIjoyMDM4MjMzNTM1fQ.V5E-PEevypfXVsTX820ys54k_cbNTza7u0TPX8vehxw",
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)

	//go func() {
	//	for msg := range c.Receive {
	//		log.Info().Interface("msg", msg).Msg("Received message")
	//	}
	//
	//	log.Info().Msg("Receive channel closed")
	//}()

	// implement a signal handler to call cancel() when the program is interrupted

	if err := c.Connect(ctx); err != nil {
		log.Err(err).Msg("Failed to connect to Home Assistant")
		cancel()
		return
	}

	c.WaitAuthenticated(ctx)

	events, err := c.SubscribeEvents(ctx)
	if err != nil {
		log.Err(err).Msg("Failed to subscribe to events")
		cancel()
		return
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Info().Msg("Shutting down event loop")
				return
			case event := <-events:
				log.Info().Interface("event", event.String()).Msg("Received event")
			}
		}
	}()

	// implement a signal handler to call cancel() when the program is interrupted

	<-ctx.Done()
	log.Info().Msg("Shutting down")

	if err := c.Close(); err != nil {
		log.Err(err).Msg("Failed to close Home Assistant connection")
	}
}

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/jkaflik/hass2ch/hass"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	logLevel = flag.String("log-level", "info", "Log level")

	host   = flag.String("host", "homeassistant.local", "Home Assistant host")
	secure = flag.Bool("secure", false, "Use secure connection")
)

func client(ctx context.Context) (*hass.Client, error) {
	schema := "ws"
	if *secure {
		schema = "wss"
	}

	url := fmt.Sprintf("%s://%s", schema, *host)

	token := os.Getenv("HASS_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("HASS_TOKEN environment variable not set")
	}

	c := &hass.Client{
		Host:  url,
		Token: token,
	}

	if err := c.Connect(ctx); err != nil {
		return nil, err
	}

	return c, c.WaitAuthenticated(ctx)
}

func dumpEvents(ctx context.Context, c *hass.Client) {
	cv, err := c.SubscribeEvents(ctx)

	if err != nil {
		log.Fatal().Err(err).Msg("Failed to subscribe to events")
		return
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case v := <-cv:
				fmt.Println(v)
				log.Info().Msg("Event received")
			}
		}
	}()
}

func main() {
	ll, err := zerolog.ParseLevel(*logLevel)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to parse log level")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	log.Logger.Level(ll)

	flag.Parse()
	args := flag.Args()

	if len(args) == 0 || args[0] == "help" {
		fmt.Println("Usage: hass2ch [command]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  help  Show this help message")
		fmt.Println("  dump  Dump events to stdout")
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	c, err := client(ctx)
	if err != nil {
		log.Err(err).Msg("Failed to create Home Assistant client")
		return
	}

	switch {
	case args[0] == "dump":
		dumpEvents(ctx, c)
	default:
		log.Fatal().Msgf("Unknown command: %s", args[0])
	}

	<-ctx.Done()
	log.Info().Msg("Shutting down")

	if err := c.Close(); err != nil {
		log.Err(err).Msg("Failed to close Home Assistant connection")
	}
}

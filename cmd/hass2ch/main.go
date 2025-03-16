package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/goccy/go-json"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/jkaflik/hass2ch/hass"
	"github.com/jkaflik/hass2ch/internal/ingestion"
	"github.com/jkaflik/hass2ch/internal/metrics"
	"github.com/jkaflik/hass2ch/pkg/clickhouse"
)

var (
	logLevel  = flag.String("log-level", "info", "Log level")
	prettyLog = flag.Bool("pretty-log", false, "Enable pretty console logging instead of JSON")

	// Home Assistant connection
	host   = flag.String("host", "homeassistant.local", "Home Assistant host")
	secure = flag.Bool("secure", false, "Use secure connection")

	// ClickHouse connection
	chUrl      = flag.String("clickhouse-url", "http://localhost:8123", "ClickHouse HTTP URL")
	chDatabase = flag.String("clickhouse-database", "hass", "ClickHouse database")
	chUsername = flag.String("clickhouse-username", "default", "ClickHouse username")
	chPassword = flag.String("clickhouse-password", "", "ClickHouse password. It can also be set via CLICKHOUSE_PASSWORD environment variable")

	// ClickHouse retry settings
	chMaxRetries      = flag.Int("clickhouse-max-retries", 5, "Maximum number of retries for ClickHouse operations")
	chInitialInterval = flag.Duration("clickhouse-initial-interval", 500*time.Millisecond, "Initial retry interval for ClickHouse operations")
	chMaxInterval     = flag.Duration("clickhouse-max-interval", 30*time.Second, "Maximum retry interval for ClickHouse operations")
	chTimeout         = flag.Duration("clickhouse-timeout", 60*time.Second, "Timeout for ClickHouse operations")

	// Metrics server
	metricsAddr   = flag.String("metrics-addr", ":9090", "Address to expose Prometheus metrics on")
	enableMetrics = flag.Bool("enable-metrics", true, "Enable Prometheus metrics server")
)

func hassClient(ctx context.Context) (*hass.Client, error) {
	schema := "ws"
	if *secure {
		schema = "wss"
	}

	url := fmt.Sprintf("%s://%s", schema, *host)

	token := os.Getenv("HASS_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("HASS_TOKEN environment variable not set")
	}

	// Create client with reconnection settings
	c := hass.NewClient(
		url,
		token,
		hass.WithReconnectConfig(
			1*time.Second,  // Initial reconnect interval
			30*time.Second, // Max reconnect interval
			1.5,            // Backoff multiplier
		),
	)

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
			case event := <-cv:
				log.Info().
					Str("event_type", string(event.Event.EventType)).
					Str("entity_id", event.Event.Data.EntityID).
					Msg("Event received")
				// Print the full event structure as JSON for debugging
				if jsonData, err := json.MarshalIndent(event, "", "  "); err == nil {
					fmt.Println(string(jsonData))
				}
			}
		}
	}()
}

//nolint:gocyclo
func main() {
	flag.Parse()
	args := flag.Args()

	ll, err := zerolog.ParseLevel(*logLevel)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to parse log level")
	}

	if *prettyLog {
		// Use console writer for pretty output
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	} else {
		// Use JSON logging by default
		zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
		log.Logger = zerolog.New(os.Stderr).With().Timestamp().Logger()
	}
	log.Logger.Level(ll)

	if len(args) == 0 || args[0] == "help" {
		fmt.Println("Usage: hass2ch [command]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  help     Show this help message")
		fmt.Println("  dump     Dump events to stdout")
		fmt.Println("  pipeline Run the ingestion pipeline (Home Assistant to ClickHouse)")
		return
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Start metrics server if enabled
	var metricsServer *metrics.Server
	if *enableMetrics {
		metricsServer = metrics.NewServer(*metricsAddr)
		go func() {
			if err := metricsServer.Start(); err != nil {
				log.Error().Err(err).Msg("Failed to start metrics server")
			}
		}()
		log.Info().Str("addr", *metricsAddr).Msg("Started metrics server")
	}

	c, err := hassClient(ctx)
	if err != nil {
		log.Err(err).Msg("Failed to create Home Assistant client")
		return
	}

	switch args[0] {
	case "dump":
		dumpEvents(ctx, c)
	case "pipeline":
		// Create custom HTTP client with timeout
		httpClient := &http.Client{
			Timeout: *chTimeout,
		}

		// Configure retry settings
		retryConfig := clickhouse.RetryConfig{
			MaxRetries:          *chMaxRetries,
			InitialInterval:     *chInitialInterval,
			MaxInterval:         *chMaxInterval,
			Multiplier:          2.0,
			RandomizationFactor: 0.5,
		}

		if *chPassword == "" && os.Getenv("CLICKHOUSE_PASSWORD") != "" {
			*chPassword = os.Getenv("CLICKHOUSE_PASSWORD")
		}

		// Create ClickHouse client with retry capabilities
		chClient, err := clickhouse.NewClient(
			*chUrl,
			*chUsername,
			*chPassword,
			clickhouse.WithHTTPClient(httpClient),
			clickhouse.WithRetryConfig(retryConfig),
		)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to create ClickHouse client")
			return
		}

		log.Info().
			Int("max_retries", *chMaxRetries).
			Dur("initial_interval", *chInitialInterval).
			Dur("max_interval", *chMaxInterval).
			Msg("Configured ClickHouse client with retry capabilities")

		// Create and run the pipeline
		pipeline := ingestion.NewPipeline(chClient, c, *chDatabase)
		log.Info().Str("database", *chDatabase).Msg("Starting ingestion pipeline")

		if err := pipeline.Run(ctx); err != nil {
			log.Fatal().Err(err).Msg("Pipeline failed")
			return
		}
	default:
		log.Fatal().Msgf("Unknown command: %s", args[0])
	}

	<-ctx.Done()
	log.Info().Msg("Shutting down")

	// Shutdown metrics server gracefully
	if metricsServer != nil {
		if err := metricsServer.Shutdown(context.Background()); err != nil {
			log.Error().Err(err).Msg("Failed to shutdown metrics server")
		}
	}

	if err := c.Close(); err != nil {
		log.Err(err).Msg("Failed to close Home Assistant connection")
	}
}

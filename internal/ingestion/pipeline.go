package ingestion

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/jkaflik/hass2ch/hass"
	"github.com/jkaflik/hass2ch/internal/metrics"
	"github.com/jkaflik/hass2ch/pkg/channel"
	"github.com/jkaflik/hass2ch/pkg/clickhouse"
	"github.com/jkaflik/hass2ch/pkg/clickhouse/format"
)

type Pipeline struct {
	chClient   *clickhouse.Client
	hassClient *hass.Client
	database   string

	tableExists map[string]bool
}

func NewPipeline(chClient *clickhouse.Client, hassClient *hass.Client, database string) *Pipeline {
	return &Pipeline{
		chClient:   chClient,
		hassClient: hassClient,
		database:   database,
	}
}

func (p *Pipeline) Run(ctx context.Context) error {
	log.Info().Msg("starting pipeline")

	// Set connection status metrics
	metrics.HassConnectionStatus.Set(1)
	metrics.CHConnectionStatus.Set(1)

	// Track database operations
	startTime := time.Now()
	if err := p.createDatabaseIfNotExists(ctx); err != nil {
		metrics.DatabaseOperationsTotal.WithLabelValues("create_database", "error").Inc()
		return fmt.Errorf("failed to create database: %w", err)
	}
	metrics.DatabaseOperationsTotal.WithLabelValues("create_database", "success").Inc()
	metrics.CHQueryDuration.WithLabelValues("create_database").Observe(time.Since(startTime).Seconds())

	p.tableExists = make(map[string]bool)

	eventsChan, err := p.hassClient.SubscribeEvents(ctx, hass.SubscribeEventsWithEventType(hass.EventTypeStateChanged))
	if err != nil {
		metrics.HassConnectionStatus.Set(0)
		return fmt.Errorf("failed to get states: %w", err)
	}

	// Create a wrapper that counts received events
	countedEventsChan := make(chan *hass.EventMessage)
	go func() {
		defer close(countedEventsChan)
		for event := range eventsChan {
			metrics.EventsReceived.Inc()
			countedEventsChan <- event
		}
	}()

	// Filter only state_changed events
	stateChangeChan := channel.Buffered(
		channel.Filter(countedEventsChan, func(event *hass.EventMessage) bool {
			if event.Event.EventType == hass.EventTypeStateChanged {
				return true
			}

			metrics.EventsFiltered.Inc()
			log.Debug().Str("event_type", string(event.Event.EventType)).Msg("unsupported event type")
			return false
		}),
		1_000,
	)

	// Batch state change events by entity domain
	stateChangeBatch, errChan := channel.Batch(stateChangeChan, channel.BatchOptions[*hass.EventMessage]{
		MaxSize:     100_000,
		MaxWait:     time.Second * 1,
		PartitionBy: partitionByStateChangeEntityDomain,
	})

	for {
		select {
		case <-ctx.Done():
			log.Info().Err(ctx.Err()).Msg("pipeline has been stopped")
			metrics.HassConnectionStatus.Set(0)
			metrics.CHConnectionStatus.Set(0)
			return nil
		case err, ok := <-errChan:
			if !ok {
				continue
			}
			log.Error().Err(err).Msg("failed to batch events")
		case batch, ok := <-stateChangeBatch:
			if !ok {
				log.Debug().Msg("batch state change channel has been closed")
				return nil
			}

			// Record batch size
			metrics.BatchSize.Observe(float64(len(batch)))
			metrics.BatchesProcessed.Inc()

			// Track batch processing time
			batchStart := time.Now()
			p.handleStateChangeBatch(ctx, batch)
			metrics.BatchProcessingDuration.Observe(time.Since(batchStart).Seconds())
		}
	}
}

func (p *Pipeline) handleStateChangeBatch(ctx context.Context, batch []*hass.EventMessage) {
	values := make([]any, 0, len(batch))
	database := p.database
	var tableName string
	processedCount := 0
	errorCount := 0

	for _, event := range batch {
		insert, err := resolveInput(event)
		if err != nil {
			log.Warn().Err(err).Msg("failed to resolve input for event")
			errorCount++
			continue
		}

		if insert.Database != p.database {
			log.Error().Str("database", insert.Database).Str("conflict", p.database).Msg("conflicting databases")
			errorCount++
			continue
		}

		if tableName == "" {
			tableName = insert.TableName
		} else if tableName != insert.TableName {
			log.Error().Str("tableName", insert.TableName).Str("conflict", tableName).Msg("conflicting table names")
			errorCount++
			continue
		}

		values = append(values, insert.Input)
		processedCount++

		tableKey := fmt.Sprintf("%s.%s", insert.Database, insert.TableName)
		if _, ok := p.tableExists[tableKey]; ok {
			continue
		}

		log.Info().Str("table", tableKey).Msg("creating table")

		// Get state type for table creation
		stateChangeDomain := extractDomainFromState(event.Event.Data.NewState)
		stateType := resolveStateChangeType(stateChangeDomain)

		// Time table creation
		startTime := time.Now()
		if err := createStateChangeTable(ctx, p.chClient, insert.Database, insert.TableName, stateType); err != nil {
			metrics.DatabaseOperationsTotal.WithLabelValues("create_table", "error").Inc()
			log.Error().Err(err).
				Str("database", insert.Database).
				Str("table", insert.TableName).
				Msg("failed to create table")
			continue
		}
		metrics.DatabaseOperationsTotal.WithLabelValues("create_table", "success").Inc()
		metrics.CHQueryDuration.WithLabelValues("create_table").Observe(time.Since(startTime).Seconds())
		p.tableExists[tableKey] = true
	}

	r := format.NewJSONEachRowReader(values)

	if r.Len() == 0 {
		return
	}

	query := fmt.Sprintf("INSERT INTO %s.%s FORMAT JSONEachRow", database, tableName)

	// Time the insert operation
	startTime := time.Now()
	if err := p.chClient.Execute(ctx, query, r); err != nil {
		metrics.DatabaseOperationsTotal.WithLabelValues("insert", "error").Inc()
		metrics.EventsProcessed.Add(float64(errorCount))
		log.Error().Err(err).
			Str("database", database).
			Str("table", tableName).
			Int("rows", len(values)).
			Msg("failed to insert data")
	} else {
		metrics.DatabaseOperationsTotal.WithLabelValues("insert", "success").Inc()
		metrics.EventsProcessed.Add(float64(processedCount))
		metrics.CHQueryDuration.WithLabelValues("insert").Observe(time.Since(startTime).Seconds())
		log.Info().
			Str("database", database).
			Str("table", tableName).
			Int("rows", len(values)).
			Msg("inserted data")
	}
}

func (p *Pipeline) createDatabaseIfNotExists(ctx context.Context) error {
	return p.chClient.Execute(ctx, fmt.Sprintf("CREATE DATABASE IF NOT EXISTS %s", p.database), nil)
}

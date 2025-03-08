package ingestion

import (
	"context"
	"github.com/jkaflik/hass2ch/ch"
	"github.com/jkaflik/hass2ch/hass"
	"github.com/rs/zerolog/log"
	"github.com/valyala/fastjson"
)

type Pipeline struct {
	chClient   *ch.Client
	hassClient *hass.Client
}

func (p *Pipeline) Run(ctx context.Context) error {
	stateChangeEvents, err := p.hassClient.GetStates(ctx)

	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case event := <-stateChangeEvents:
			p.handleStateChange(event)
		}
	}
}

func (p *Pipeline) handleStateChange(event *fastjson.Value) {
	insert, err := resolveInput(event)

	if err != nil {
		log.Error().Err(err).Msg("failed to resolve input")
		return
	}

	err = p.chClient.Insert(insert.query, insert.data)

	if err != nil {
		log.Error().Err(err).Msg("failed to insert data")
	}
}

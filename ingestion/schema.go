package ingestion

import (
	"errors"
	"fmt"

	"github.com/ClickHouse/ch-go/proto"
	"github.com/jkaflik/hass2ch/hass"
	"github.com/valyala/fastjson"
)

func PartitionByEventStateDomain(v *fastjson.Value) (string, error) {
	return string(v.GetStringBytes("event.data.new_state.domain")), nil
}

func ResolveInput(chan *fastjson.Value) (proto.Input, error) {
	eventType := hass.EventType(v.GetStringBytes("event.type"))

	switch eventType {
	case hass.EventTypeStateChanged:
		return resolveStateChangeInput(v)
	default:
		return proto.Input{}, fmt.Errorf("unsupported event type: %s", eventType)
	}
}

func resolveStateChangeInput(v *fastjson.Value) (proto.Input, error) {
	state := v.Get("event.data.new_state")
	if state == nil || !state.Exists() {
		return proto.Input{}, errors.New("event.data.new_state is missing")
	}

}

type Destination struct {
	TableName string
	Input     proto.Input
}

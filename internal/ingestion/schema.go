package ingestion

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jkaflik/hass2ch/hass"
	"github.com/valyala/fastjson"
)

func partitionByEventEntityID(v *fastjson.Value) (string, error) {
	entityID := v.Get("event.data.new_state.entity_id")
	if entityID == nil {
		return "", errors.New("event.data.new_state.entity_id is missing")
	}
	b, err := entityID.StringBytes()
	return string(b), err
}

func resolveInput(v *fastjson.Value) (*insert, error) {
	eventType := hass.EventType(v.GetStringBytes("event.event_type"))

	switch eventType {
	case hass.EventTypeStateChanged:
		return resolveStateChangeDestination(v)
	default:
		return nil, fmt.Errorf("unsupported event type: %s", eventType)
	}
}

func resolveStateChangeDestination(v *fastjson.Value) (*insert, error) {
	data := v.Get("event.data")
	if data == nil {
		return nil, errors.New("event.data is missing")
	}

	entityID := string(data.GetStringBytes("entity_id"))
	if entityID == "" {
		return nil, errors.New("event.data.entity_id is missing")
	}

	oldState := data.Get("old_state")
	if oldState == nil {
		return nil, errors.New("event.data.old_state is missing")
	}

	newState := data.Get("new_state")
	if newState == nil {
		return nil, errors.New("event.data.new_state is missing")
	}

	domainName, _, found := strings.Cut(entityID, ".")
	if !found {
		return nil, errors.New("entity ID is missing a domain")
	}

	input, err := resolveStateChangeInput(oldState, newState)
	if err != nil {
		return nil, err
	}

	return &insert{
		TableName: entityIdToTableName(string(entityID)),
		Input:     input,
	}, nil
}

func resolveStateChangeInput(oldState, newState *fastjson.Value) (*fastjson.Value, error) {
	stateChange := &fastjson.Value{}
	stateChange.Set("entity_id", newState.Get("entity_id"))
	stateChange.Set("state", newState.Get("state"))
	stateChange.Set("old_state", oldState.Get("state"))
	stateChange.Set("attributes", newState.Get("attributes"))
	stateChange.Set("context", newState.Get("context"))
	stateChange.Set("last_changed", newState.Get("last_changed"))
	stateChange.Set("last_updated", newState.Get("last_updated"))
	stateChange.Set("last_reported", newState.Get("last_reported"))

	return stateChange, nil
}

func resolveStateChangeType(domainName string, newState *fastjson.Value) (string, error) {
	switch domainName {
	case "binary_sensor", "switch", "input_boolean":
		return "Bool", nil
	case "light", "automation", "scene", "script":
		return "LowCardinality(String)", nil
	case "climate":
		return "LowCardinality(String)", nil
	case "sensor":
		// Try to determine if it's a numeric sensor
		if state := newState.Get("state"); state != nil {
			if _, err := state.Float64(); err == nil {
				return "Nullable(Float64)", nil
			}
		}
		return "String", nil
	case "number", "input_number":
		return "Nullable(Float64)", nil
	case "counter":
		return "Int64", nil
	case "input_datetime", "timer":
		return "DateTime", nil
	case "person", "device_tracker":
		return "LowCardinality(String)", nil
	case "weather":
		return "LowCardinality(String)", nil
	default:
		return "String", nil
	}
}

// entityIdToTableName converts an entity ID to a table name.
// The entity ID is expected to be in the format domain.object_id.
// The table name is the entity ID with the dots replaced by underscores.
func entityIdToTableName(entityID string) string {
	return strings.Replace(entityID, ".", "_", -1)
}

type insert struct {
	Database  string
	TableName string
	Input     *fastjson.Value
}

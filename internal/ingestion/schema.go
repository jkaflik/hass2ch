package ingestion

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jkaflik/hass2ch/hass"
	"github.com/jkaflik/hass2ch/pkg/clickhouse"
)

// StateChange represents a processed state change event ready for insertion into ClickHouse
type StateChange struct {
	EntityID     string `json:"entity_id"`
	State        any    `json:"state"`
	OldState     any    `json:"old_state"`
	Attributes   any    `json:"attributes"`
	Context      any    `json:"context"`
	LastChanged  string `json:"last_changed"`
	LastUpdated  string `json:"last_updated"`
	LastReported string `json:"last_reported,omitempty"`
}

func partitionByStateChangeEntityDomain(event *hass.EventMessage) (string, error) {
	if event.Event.Data.NewState == nil || event.Event.Data.NewState.EntityID == "" {
		return "", errors.New("event.data.new_state.entity_id is missing")
	}
	return extractDomainFromState(event.Event.Data.NewState), nil
}

func resolveInput(event *hass.EventMessage) (*insert, error) {
	switch event.Event.EventType {
	case hass.EventTypeStateChanged:
		return resolveStateChangeDestination(event)
	default:
		return nil, fmt.Errorf("unsupported event type: %s", event.Event.EventType)
	}
}

func resolveStateChangeDestination(event *hass.EventMessage) (*insert, error) {
	data := event.Event.Data

	if data.EntityID == "" {
		return nil, errors.New("event.data.entity_id is missing")
	}

	if data.OldState == nil {
		return nil, errors.New("event.data.old_state is missing")
	}

	if data.NewState == nil {
		return nil, errors.New("event.data.new_state is missing")
	}

	domain := extractDomainFromState(event.Event.Data.NewState)

	input, err := resolveStateChangeInput(domain, data.OldState, data.NewState)
	if err != nil {
		return nil, err
	}

	return &insert{
		Database:  "hass", // Default database, will be overridden in pipeline if needed
		TableName: domain,
		Input:     input,
	}, nil
}

func resolveStateChangeInput(domain string, oldState, newState *hass.State) (*StateChange, error) {
	var oldStateValue any = oldState.State
	var newStateValue any = newState.State

	if isSkippedValue(oldStateValue) {
		oldStateValue = ""
	}
	if isSkippedValue(newStateValue) {
		return nil, fmt.Errorf("skipping event with unknown state: %s", newStateValue)
	}

	switch domain {
	case hass.EntityBinarySensor, hass.EntitySwitch, hass.EntityInputBoolean, hass.EntityBooleanSensor:
		oldStateValue = normalizeBooleanValue(oldState.State)
		newStateValue = normalizeBooleanValue(newState.State)
	}

	stateChange := &StateChange{
		EntityID:    newState.EntityID,
		State:       newStateValue,
		OldState:    oldStateValue,
		Attributes:  newState.Attributes,
		Context:     newState.Context,
		LastChanged: newState.LastChanged.Format(time.RFC3339Nano),
		LastUpdated: newState.LastUpdated.Format(time.RFC3339Nano),
	}

	if newState.LastReported != nil {
		stateChange.LastReported = newState.LastReported.Format(time.RFC3339Nano)
	}

	return stateChange, nil
}

func resolveStateChangeType(domainName string) string {
	switch domainName {
	case hass.EntityBinarySensor,
		hass.EntitySwitch,
		hass.EntityInputBoolean,
		hass.EntityBooleanSensor:
		return "Bool"
	case hass.EntityLight,
		hass.EntityAutomation,
		hass.EntityScene,
		hass.EntityScript,
		hass.EntitySun,
		hass.EntityDeviceTracker,
		hass.EntityPerson,
		hass.EntityZone,
		hass.EntityWeather,
		hass.EntityClimate:
		return "LowCardinality(String)"
	case hass.EntitySensor:
		return "String"
	case hass.EntityNumericSensor:
		return "Float64"
	case hass.EntityNumber,
		hass.EntityInputNumber:
		return "Nullable(Float64)"
	case hass.EntityCounter:
		return "Int64"
	case hass.EntityInputDateTime,
		hass.EntityTimer,
		hass.EntityImage:
		return "DateTime"
	default:
		return "String"
	}
}

// extractDomainFromState extracts the domain from an entity ID.
// The entity ID is expected to be in the format domain.object_id.
// If the domain is sensor and the state is a number, the domain is changed to numeric_sensor.
func extractDomainFromState(state *hass.State) string {
	domain, _, _ := strings.Cut(state.EntityID, ".")

	if domain == hass.EntitySensor {
		switch state.State {
		case "on", "true", "off", "false":
			return hass.EntityBinarySensor
		default:
		}

		if _, err := strconv.ParseFloat(state.State, 64); err == nil {
			return hass.EntityNumericSensor
		}
	}

	return domain
}

type insert struct {
	Database  string
	TableName string
	Input     interface{}
}

// createStateChangeTable creates a table for a state change event in ClickHouse
func createStateChangeTable(ctx context.Context, client *clickhouse.Client, database, tableName string, stateType string) error {
	query := fmt.Sprintf(stateChangeDDL, database, tableName, stateType, stateType)
	return client.Execute(ctx, query, nil)
}

func normalizeBooleanValue(value string) any {
	switch value {
	case hass.BooleanOnValue, hass.BooleanTrueValue:
		return true
	case hass.BooleanOffValue, hass.BooleanFalseValue:
		return false
	default:
		return false
	}
}

func isSkippedValue(value any) bool {
	switch value {
	case "", hass.UnknownValue, hass.UnavailableValue:
		return true
	default:
	}

	return false
}

package ingestion

const (
	stateChangeDDL = `
CREATE TABLE IF NOT EXISTS %s.%s (
    entity_id LowCardinality(String),
    state %s,
    old_state %s,
    attributes JSON,
    context JSON,
    last_changed DateTime64(3, 'UTC'),
    last_updated DateTime64(3, 'UTC'),
    last_reported DateTime64(3, 'UTC'),
    received_at DateTime64(3, 'UTC') DEFAULT now64(3)
) ENGINE = MergeTree()
PARTITION BY toYYYYMM(last_updated)
ORDER BY (entity_id, last_updated)
SETTINGS index_granularity = 8192;`
)

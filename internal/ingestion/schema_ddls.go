package ingestion

const (
	stateChangeDDL = `
CREATE TABLE IF NOT EXISTS %s.%s (
    entity_id String,
    state %s,
    old_state %s,
    attributes JSON,
    context JSON,
    last_changed DateTime64(3, 'UTC'),
    last_updated DateTime64(3, 'UTC'),
    last_reported DateTime64(3, 'UTC')
) ENGINE = MergeTree()
PARTITION BY toYYYYMM(date)
ORDER BY (entity_id, received_at)
SETTINGS index_granularity = 8192;`
)

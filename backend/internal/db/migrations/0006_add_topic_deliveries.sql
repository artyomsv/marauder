-- +goose Up
-- +goose StatementBegin

-- topic_deliveries records every torrent Marauder has pushed to a client
-- for a topic. The infohash is the universal key correlating a delivery
-- with live client status (qBittorrent hashes=, Transmission hashString).
-- label is the human-readable form: "s02e06" for episodic trackers, the
-- release/display name for single-torrent topics.
CREATE TABLE topic_deliveries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    topic_id     UUID NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
    infohash     TEXT NOT NULL,
    label        TEXT NOT NULL DEFAULT '',
    client_id    UUID,
    delivered_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_topic_deliveries_topic ON topic_deliveries (topic_id, delivered_at DESC);

-- A topic delivers a given infohash exactly once; re-detection of the same
-- release must not duplicate the row. The scheduler inserts with
-- ON CONFLICT DO NOTHING to make redelivery idempotent.
CREATE UNIQUE INDEX idx_topic_deliveries_topic_infohash
    ON topic_deliveries (topic_id, infohash);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS topic_deliveries;
-- +goose StatementEnd

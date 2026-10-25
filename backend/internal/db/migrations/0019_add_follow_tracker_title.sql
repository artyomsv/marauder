-- +goose Up
-- +goose StatementBegin
-- Per-topic "follow the tracker's topic title" (the setting floated in issue
-- #86 and deliberately scoped out of the self-heal gate in #90): when set, the
-- scheduler keeps the topic's display name in sync with the title the tracker
-- reports on each check. Off by default, so a resolved title stays locked
-- exactly as #90 left it. A user rename clears it (same rule as
-- display_name_is_placeholder): typing a name is taking ownership of it.
ALTER TABLE topics
    ADD COLUMN follow_tracker_title BOOLEAN NOT NULL DEFAULT false;
-- The instance-wide default applied to topics created without an explicit
-- choice (API clients, the Sonarr poller). The per-topic column materialises
-- it at create time, so changing this later never touches existing topics.
ALTER TABLE settings
    ADD COLUMN follow_tracker_title_default BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE settings
    DROP COLUMN follow_tracker_title_default;
ALTER TABLE topics
    DROP COLUMN follow_tracker_title;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS custom_blueprints (
    name           TEXT    NOT NULL PRIMARY KEY,
    display_name   TEXT    NOT NULL,
    description    TEXT    NOT NULL DEFAULT '',
    blueprint_yaml TEXT    NOT NULL,
    created_at     INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at     INTEGER NOT NULL DEFAULT (unixepoch())
);

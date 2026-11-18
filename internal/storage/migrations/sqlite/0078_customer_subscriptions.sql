CREATE TABLE IF NOT EXISTS customer_subscriptions (
    id TEXT PRIMARY KEY,
    telegram_id BIGINT NOT NULL,
    panel_ref TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT 'migrated',
    expire_at TEXT NOT NULL DEFAULT '',
    plan_snapshot TEXT NOT NULL DEFAULT '',
    UNIQUE (telegram_id, id)
);

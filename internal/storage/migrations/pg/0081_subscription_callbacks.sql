CREATE TABLE IF NOT EXISTS subscription_callbacks (
    token TEXT PRIMARY KEY,
    telegram_id BIGINT NOT NULL,
    subscription_id TEXT NOT NULL,
    callback_data TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    UNIQUE (telegram_id, subscription_id, callback_data)
);

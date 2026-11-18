CREATE TABLE IF NOT EXISTS subscription_selections (
    telegram_id BIGINT PRIMARY KEY,
    subscription_id TEXT NOT NULL,
    FOREIGN KEY (telegram_id, subscription_id)
        REFERENCES customer_subscriptions (telegram_id, id)
        ON UPDATE CASCADE ON DELETE CASCADE
);

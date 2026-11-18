CREATE TABLE IF NOT EXISTS subscription_operations (
    method TEXT NOT NULL,
    operation_id TEXT NOT NULL,
    telegram_id BIGINT NOT NULL,
    subscription_id TEXT NOT NULL,
    amount TEXT NOT NULL,
    plan_snapshot TEXT NOT NULL,
    phase TEXT NOT NULL,
    payment_ref TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    expected_expiry TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (method, operation_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS subscription_operation_inflight
    ON subscription_operations (telegram_id, subscription_id)
    WHERE phase = 'executing';

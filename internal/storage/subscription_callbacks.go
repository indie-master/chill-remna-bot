package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"time"

	"remnabot/internal/model"
)

type SubscriptionCallbackStorage interface {
	SubscriptionCallbackToken(context.Context, int64, string, string) (string, error)
	SubscriptionCallback(context.Context, int64, string) (*model.SubscriptionCallback, error)
}

func (b *base) SubscriptionCallbackToken(ctx context.Context, owner int64, id, data string) (string, error) {
	if len(data) == 0 || len(data) > 64 {
		return "", ErrSubscriptionData
	}
	expires := time.Now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339)
	var token string
	err := b.db.QueryRowContext(ctx,
		"INSERT INTO subscription_callbacks (token, telegram_id, subscription_id, callback_data, expires_at) "+
			"SELECT "+b.ph(1)+", telegram_id, id, "+b.ph(2)+", "+b.ph(3)+" FROM customer_subscriptions WHERE telegram_id = "+b.ph(4)+" AND id = "+b.ph(5)+
			" ON CONFLICT (telegram_id, subscription_id, callback_data) DO UPDATE SET expires_at = excluded.expires_at RETURNING token",
		rand.Text(), data, expires, owner, id).Scan(&token)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSubscriptionOwner
	}
	return token, err
}

func (b *base) SubscriptionCallback(ctx context.Context, owner int64, token string) (*model.SubscriptionCallback, error) {
	var cb model.SubscriptionCallback
	err := b.db.QueryRowContext(ctx,
		"SELECT token, telegram_id, subscription_id, callback_data, expires_at FROM subscription_callbacks WHERE telegram_id = "+b.ph(1)+" AND token = "+b.ph(2)+" AND expires_at > "+b.ph(3), owner, token, nowStr()).
		Scan(&cb.Token, &cb.TelegramID, &cb.SubscriptionID, &cb.Data, &cb.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &cb, err
}

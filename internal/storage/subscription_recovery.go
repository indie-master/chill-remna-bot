package storage

import (
	"context"
	"time"

	"remnabot/internal/model"
)

// Persist the exact outbound expiry before contacting the panel. A lost response
// can then be reconciled without extending the subscription a second time.
func (b *base) SetSubscriptionExpectedExpiry(ctx context.Context, method, id, expiry string) error {
	if _, err := time.Parse(time.RFC3339, expiry); err != nil {
		return ErrSubscriptionData
	}
	r, err := b.db.ExecContext(ctx, "UPDATE subscription_operations SET expected_expiry = "+b.ph(1)+
		" WHERE method = "+b.ph(2)+" AND operation_id = "+b.ph(3)+" AND phase = 'executing' AND (expected_expiry = '' OR expected_expiry = "+b.ph(4)+")", expiry, method, id, expiry)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n != 1 {
		return ErrSubscriptionHeld
	}
	return err
}

func (b *base) PendingSubscriptionOperations(ctx context.Context) ([]model.SubscriptionOperation, error) {
	rows, err := b.db.QueryContext(ctx, "SELECT "+subscriptionOperationCols+" FROM subscription_operations WHERE phase = 'executing' ORDER BY created_at LIMIT 50")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SubscriptionOperation
	for rows.Next() {
		var op model.SubscriptionOperation
		var raw string
		if err := rows.Scan(&op.Method, &op.ID, &op.TelegramID, &op.SubscriptionID, &op.Amount, &raw, &op.Phase, &op.PaymentRef, &op.CreatedAt, &op.ExpectedExpiry); err != nil {
			return nil, err
		}
		op.Snapshot = model.DecodePlanSnapshot(raw)
		out = append(out, op)
	}
	return out, rows.Err()
}

// Cancellation and any internal-balance refund commit together, at most once.
// Provider refunds are deliberately not initiated here: an administrator must
// verify them in the provider before confirming cancellation in the UI.
func (b *base) CancelSubscriptionOperation(ctx context.Context, method, id string) error {
	op, err := b.SubscriptionOperation(ctx, method, id)
	if err != nil {
		return err
	}
	if op == nil || op.Snapshot == nil {
		return ErrSubscriptionData
	}
	if op.Phase == "cancelled" {
		return nil
	}
	if op.Phase != "executing" {
		return ErrSubscriptionHeld
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	r, err := tx.ExecContext(ctx, "UPDATE subscription_operations SET phase = 'cancelled' WHERE method = "+b.ph(1)+" AND operation_id = "+b.ph(2)+" AND phase = 'executing'", method, id)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrSubscriptionHeld
	}
	if method == "balance" {
		if op.Snapshot.BalanceKopecks <= 0 {
			return ErrSubscriptionData
		}
		r, err = tx.ExecContext(ctx, "UPDATE users SET balance = balance + "+b.ph(1)+" WHERE telegram_id = "+b.ph(2), op.Snapshot.BalanceKopecks, op.TelegramID)
		if err != nil {
			return err
		}
		n, err = r.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrSubscriptionOwner
		}
	}
	return tx.Commit()
}

package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"remnabot/internal/model"
)

var (
	ErrSubscriptionHeld      = errors.New("subscription operation is awaiting verification")
	ErrSubscriptionUncertain = errors.New("subscription result is uncertain; funds are reserved for verification")
)

type SubscriptionOperationStorage interface {
	IssueSubscriptionInvoice(context.Context, *model.SubscriptionOperation) error
	SubscriptionOperation(context.Context, string, string) (*model.SubscriptionOperation, error)
	ClaimSubscriptionOperation(context.Context, *model.SubscriptionOperation) error
	CommitSubscriptionOperation(context.Context, string, *model.Payment, string) error
	SetSubscriptionExpectedExpiry(context.Context, string, string, string) error
	PendingSubscriptionOperations(context.Context) ([]model.SubscriptionOperation, error)
	CancelSubscriptionOperation(context.Context, string, string) error
}

const subscriptionOperationCols = "method, operation_id, telegram_id, subscription_id, amount, plan_snapshot, phase, payment_ref, created_at, expected_expiry"

func (b *base) insertSubscriptionOperation(ctx context.Context, op *model.SubscriptionOperation) (sql.Result, error) {
	if op == nil || op.TelegramID == 0 || op.ID == "" || op.Method == "" || op.Snapshot == nil || op.SubscriptionID != op.Snapshot.SubscriptionID {
		return nil, ErrSubscriptionData
	}
	if op.CreatedAt == "" {
		op.CreatedAt = nowStr()
	}
	return b.db.ExecContext(ctx, "INSERT INTO subscription_operations ("+subscriptionOperationCols+") VALUES ("+
		b.ph(1)+", "+b.ph(2)+", "+b.ph(3)+", "+b.ph(4)+", "+b.ph(5)+", "+b.ph(6)+", "+b.ph(7)+", "+b.ph(8)+", "+b.ph(9)+", "+b.ph(10)+") "+
		"ON CONFLICT (method, operation_id) DO NOTHING", op.Method, op.ID, op.TelegramID, op.SubscriptionID, op.Amount, op.Snapshot.Encode(), op.Phase, op.PaymentRef, op.CreatedAt, op.ExpectedExpiry)
}

func (b *base) IssueSubscriptionInvoice(ctx context.Context, op *model.SubscriptionOperation) error {
	if op == nil {
		return ErrSubscriptionData
	}
	op.Phase, op.PaymentRef = "issued", ""
	result, err := b.insertSubscriptionOperation(ctx, op)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrSubscriptionData
	}
	return nil
}

func (b *base) SubscriptionOperation(ctx context.Context, method, id string) (*model.SubscriptionOperation, error) {
	var op model.SubscriptionOperation
	var raw string
	err := b.db.QueryRowContext(ctx, "SELECT "+subscriptionOperationCols+" FROM subscription_operations WHERE method = "+b.ph(1)+" AND operation_id = "+b.ph(2), method, id).
		Scan(&op.Method, &op.ID, &op.TelegramID, &op.SubscriptionID, &op.Amount, &raw, &op.Phase, &op.PaymentRef, &op.CreatedAt, &op.ExpectedExpiry)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	op.Snapshot = model.DecodePlanSnapshot(raw)
	return &op, nil
}

func (b *base) ClaimSubscriptionOperation(ctx context.Context, op *model.SubscriptionOperation) error {
	if op == nil || op.PaymentRef == "" {
		return ErrSubscriptionData
	}
	op.Phase = "executing"
	result, err := b.insertSubscriptionOperation(ctx, op)
	if isUniqueViolation(err) {
		return ErrSubscriptionHeld
	}
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	existing, err := b.SubscriptionOperation(ctx, op.Method, op.ID)
	if err != nil {
		return err
	}
	if existing == nil || existing.TelegramID != op.TelegramID || existing.SubscriptionID != op.SubscriptionID || existing.Amount != op.Amount {
		return ErrSubscriptionOwner
	}
	if existing.Phase == "done" && existing.PaymentRef == op.PaymentRef {
		return ErrDuplicateExtID
	}
	if existing.Phase != "issued" {
		return ErrSubscriptionHeld
	}
	if !sameInvoiceTerms(existing.Snapshot, op.Snapshot) {
		return ErrSubscriptionData
	}
	result, err = b.db.ExecContext(ctx, "UPDATE subscription_operations SET phase = 'executing', payment_ref = "+b.ph(1)+
		" WHERE method = "+b.ph(2)+" AND operation_id = "+b.ph(3)+" AND telegram_id = "+b.ph(4)+" AND phase = 'issued'", op.PaymentRef, op.Method, op.ID, op.TelegramID)
	if isUniqueViolation(err) {
		return ErrSubscriptionHeld
	}
	if err != nil {
		return err
	}
	n, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrSubscriptionHeld
	}
	return nil
}

// CommitSubscriptionOperation atomically records the payment, paid terms and
// completion marker. Retrying this commit after a lost DB response is safe.
func (b *base) CommitSubscriptionOperation(ctx context.Context, operationID string, payment *model.Payment, expiry string) error {
	if payment == nil || payment.Snapshot == nil || payment.ExtID == "" {
		return ErrSubscriptionData
	}
	op, err := b.SubscriptionOperation(ctx, payment.Method, operationID)
	if err != nil {
		return err
	}
	if op == nil || op.TelegramID != payment.TelegramID || op.SubscriptionID != payment.Snapshot.SubscriptionID || op.PaymentRef != payment.ExtID {
		return ErrSubscriptionOwner
	}
	if !sameInvoiceTerms(op.Snapshot, payment.Snapshot) {
		return ErrSubscriptionData
	}
	if op.Phase == "done" {
		return nil
	}
	if op.Phase != "executing" {
		return ErrSubscriptionHeld
	}
	if payment.ID == 0 {
		payment.ID = time.Now().UnixNano()
	}
	if payment.CreatedAt == "" {
		payment.CreatedAt = nowStr()
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, "UPDATE subscription_operations SET phase = 'done' WHERE method = "+b.ph(1)+" AND operation_id = "+b.ph(2)+
		" AND telegram_id = "+b.ph(3)+" AND payment_ref = "+b.ph(4)+" AND phase = 'executing'", payment.Method, operationID, payment.TelegramID, payment.ExtID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrSubscriptionHeld
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO payments (id, telegram_id, method, months, amount, status, comment, ext_id, created_at, plan_snapshot) VALUES ("+
		b.ph(1)+", "+b.ph(2)+", "+b.ph(3)+", "+b.ph(4)+", "+b.ph(5)+", "+b.ph(6)+", "+b.ph(7)+", "+b.ph(8)+", "+b.ph(9)+", "+b.ph(10)+")",
		payment.ID, payment.TelegramID, payment.Method, payment.Months, payment.Amount, payment.Status, payment.Comment, payment.ExtID, payment.CreatedAt, payment.Snapshot.Encode())
	if err != nil {
		return err
	}
	if op.SubscriptionID != "" {
		result, err = tx.ExecContext(ctx, "UPDATE customer_subscriptions SET kind = 'paid', expire_at = "+b.ph(1)+", plan_snapshot = "+b.ph(2)+
			" WHERE telegram_id = "+b.ph(3)+" AND id = "+b.ph(4), expiry, payment.Snapshot.ForRenewal().Encode(), payment.TelegramID, op.SubscriptionID)
		if err != nil {
			return err
		}
		n, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrSubscriptionOwner
		}
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE users SET sub_expire_at = "+b.ph(1)+", notify_kind = 'paid', notify_sent = '', plan_snapshot = "+b.ph(2)+" WHERE telegram_id = "+b.ph(3), expiry, payment.Snapshot.ForRenewal().Encode(), payment.TelegramID)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func sameInvoiceTerms(a, b *model.PlanSnapshot) bool {
	return a != nil && b != nil && a.SubscriptionID == b.SubscriptionID && a.InvoiceID == b.InvoiceID &&
		a.NewAccount == b.NewAccount && a.BalanceKopecks == b.BalanceKopecks && a.Code == b.Code && a.Months == b.Months && a.Days == b.Days &&
		a.Price == b.Price && a.Currency == b.Currency && a.Fingerprint() == b.Fingerprint()
}

func (b *base) exportSubscriptionOperations(ctx context.Context, out *Snapshot) error {
	rows, err := b.db.QueryContext(ctx, "SELECT "+subscriptionOperationCols+" FROM subscription_operations ORDER BY method, operation_id")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var op model.SubscriptionOperation
		var raw string
		if err := rows.Scan(&op.Method, &op.ID, &op.TelegramID, &op.SubscriptionID, &op.Amount, &raw, &op.Phase, &op.PaymentRef, &op.CreatedAt, &op.ExpectedExpiry); err != nil {
			return err
		}
		op.Snapshot = model.DecodePlanSnapshot(raw)
		out.SubscriptionOperations = append(out.SubscriptionOperations, op)
	}
	return rows.Err()
}

package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"

	"remnabot/internal/model"
)

var (
	ErrSubscriptionOwner = errors.New("subscription is unavailable for this customer")
	ErrSubscriptionBound = errors.New("panel account is already bound")
	ErrSubscriptionData  = errors.New("invalid subscription binding")
)

// SubscriptionStorage is separate from the legacy interface to keep optional
// integrations and their adapters backwards compatible.
type SubscriptionStorage interface {
	BindSubscription(context.Context, *model.CustomerSubscription) error
	CustomerSubscriptions(context.Context, int64) ([]model.CustomerSubscription, error)
	CustomerSubscription(context.Context, int64, string) (*model.CustomerSubscription, error)
	SelectSubscription(context.Context, int64, string) error
	SelectedSubscription(context.Context, int64) (*model.CustomerSubscription, error)
	SetSubscriptionDeal(context.Context, int64, string, string, *model.PlanSnapshot) error
	RefreshSubscriptionExpiry(context.Context, *model.CustomerSubscription, string) error
}

var subscriptionRefRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func validSubscription(s *model.CustomerSubscription) bool {
	return s != nil && s.TelegramID != 0 && subscriptionRefRE.MatchString(s.PanelRef) &&
		(s.Snapshot == nil || s.Snapshot.SubscriptionID == "" || s.Snapshot.SubscriptionID == s.ID) &&
		(s.ID == "" || subscriptionRefRE.MatchString(s.ID)) && utf8.ValidString(s.Label) &&
		utf8.RuneCountInString(s.Label) <= 80 && strings.TrimSpace(s.Label) != "" &&
		!strings.ContainsAny(s.Label, "\x00\r\n")
}

// BindSubscription never reassigns an existing panel account, even when the
// caller supplies a different local ID. Ownership changes use MoveAccount.
func (b *base) BindSubscription(ctx context.Context, s *model.CustomerSubscription) error {
	if !validSubscription(s) {
		return ErrSubscriptionData
	}
	if s.ID == "" {
		s.ID = rand.Text()
	}
	if s.Snapshot != nil {
		s.Snapshot = s.Snapshot.ForRenewal()
		s.Snapshot.SubscriptionID = s.ID
	}
	if s.Kind == "" {
		s.Kind = "migrated"
	}
	if s.Kind != "migrated" && s.Kind != "trial" && s.Kind != "paid" {
		return ErrSubscriptionData
	}
	_, err := b.db.ExecContext(ctx, "INSERT INTO customer_subscriptions (id, telegram_id, panel_ref, label, expire_at, plan_snapshot, kind) VALUES ("+
		b.ph(1)+", "+b.ph(2)+", "+b.ph(3)+", "+b.ph(4)+", "+b.ph(5)+", "+b.ph(6)+", "+b.ph(7)+")",
		s.ID, s.TelegramID, s.PanelRef, s.Label, s.ExpireAt, s.Snapshot.ForRenewal().Encode(), s.Kind)
	if isUniqueViolation(err) {
		return ErrSubscriptionBound
	}
	return err
}

const subscriptionCols = "id, telegram_id, panel_ref, label, expire_at, plan_snapshot, kind"

func scanSubscription(row interface{ Scan(...any) error }) (*model.CustomerSubscription, error) {
	var s model.CustomerSubscription
	var raw string
	if err := row.Scan(&s.ID, &s.TelegramID, &s.PanelRef, &s.Label, &s.ExpireAt, &raw, &s.Kind); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	s.Snapshot = model.DecodePlanSnapshot(raw)
	return &s, nil
}

func (b *base) CustomerSubscriptions(ctx context.Context, owner int64) ([]model.CustomerSubscription, error) {
	rows, err := b.db.QueryContext(ctx, "SELECT "+subscriptionCols+" FROM customer_subscriptions WHERE telegram_id = "+b.ph(1)+" ORDER BY label, id", owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.CustomerSubscription{}
	for rows.Next() {
		s, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *s)
	}
	return result, rows.Err()
}

func (b *base) CustomerSubscription(ctx context.Context, owner int64, id string) (*model.CustomerSubscription, error) {
	return scanSubscription(b.db.QueryRowContext(ctx, "SELECT "+subscriptionCols+" FROM customer_subscriptions WHERE telegram_id = "+b.ph(1)+" AND id = "+b.ph(2), owner, id))
}

func (b *base) SelectSubscription(ctx context.Context, owner int64, id string) error {
	result, err := b.db.ExecContext(ctx, "INSERT INTO subscription_selections (telegram_id, subscription_id) SELECT telegram_id, id FROM customer_subscriptions WHERE telegram_id = "+
		b.ph(1)+" AND id = "+b.ph(2)+" ON CONFLICT (telegram_id) DO UPDATE SET subscription_id = excluded.subscription_id", owner, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrSubscriptionOwner
	}
	return nil
}

func (b *base) SelectedSubscription(ctx context.Context, owner int64) (*model.CustomerSubscription, error) {
	return scanSubscription(b.db.QueryRowContext(ctx, "SELECT "+subscriptionCols+" FROM customer_subscriptions WHERE telegram_id = "+b.ph(1)+
		" AND id = (SELECT subscription_id FROM subscription_selections WHERE telegram_id = "+b.ph(2)+")", owner, owner))
}

func (b *base) SetSubscriptionDeal(ctx context.Context, owner int64, id, expiry string, snap *model.PlanSnapshot) error {
	if snap != nil && snap.SubscriptionID != id {
		return ErrSubscriptionData
	}
	result, err := b.db.ExecContext(ctx, "UPDATE customer_subscriptions SET expire_at = "+b.ph(1)+", plan_snapshot = "+b.ph(2)+
		" WHERE telegram_id = "+b.ph(3)+" AND id = "+b.ph(4), expiry, snap.ForRenewal().Encode(), owner, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrSubscriptionOwner
	}
	return nil
}

// A slow panel read cannot overwrite a payment committed after the read began.
func (b *base) RefreshSubscriptionExpiry(ctx context.Context, before *model.CustomerSubscription, expiry string) error {
	if before == nil {
		return ErrSubscriptionData
	}
	_, err := b.db.ExecContext(ctx, "UPDATE customer_subscriptions SET expire_at = "+b.ph(1)+
		" WHERE telegram_id = "+b.ph(2)+" AND id = "+b.ph(3)+" AND expire_at = "+b.ph(4)+" AND plan_snapshot = "+b.ph(5),
		expiry, before.TelegramID, before.ID, before.ExpireAt, before.Snapshot.Encode())
	return err
}

func (b *base) exportSubscriptions(ctx context.Context, out *Snapshot) error {
	rows, err := b.db.QueryContext(ctx, "SELECT "+subscriptionCols+" FROM customer_subscriptions ORDER BY id")
	if err != nil {
		return err
	}
	for rows.Next() {
		s, err := scanSubscription(rows)
		if err != nil {
			_ = rows.Close()
			return err
		}
		out.Subscriptions = append(out.Subscriptions, *s)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	rows, err = b.db.QueryContext(ctx, "SELECT telegram_id, subscription_id FROM subscription_selections ORDER BY telegram_id")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var selection model.SubscriptionSelection
		if err := rows.Scan(&selection.TelegramID, &selection.SubscriptionID); err != nil {
			return err
		}
		out.SubscriptionSelections = append(out.SubscriptionSelections, selection)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	return b.exportSubscriptionOperations(ctx, out)
}

func (b *base) importSubscriptions(ctx context.Context, in *Snapshot) error {
	for i := range in.Subscriptions {
		s := &in.Subscriptions[i]
		if err := b.BindSubscription(ctx, s); err != nil {
			if !errors.Is(err, ErrSubscriptionBound) {
				return err
			}
			existing, readErr := b.CustomerSubscription(ctx, s.TelegramID, s.ID)
			if readErr != nil {
				return readErr
			}
			if existing == nil || existing.PanelRef != s.PanelRef {
				return ErrSubscriptionOwner
			}
		}
	}
	for _, selection := range in.SubscriptionSelections {
		if err := b.SelectSubscription(ctx, selection.TelegramID, selection.SubscriptionID); err != nil {
			return err
		}
	}
	for i := range in.SubscriptionOperations {
		op := &in.SubscriptionOperations[i]
		if op.Phase != "issued" && op.Phase != "executing" && op.Phase != "done" && op.Phase != "cancelled" {
			return ErrSubscriptionData
		}
		if _, err := b.insertSubscriptionOperation(ctx, op); err != nil {
			return err
		}
		existing, err := b.SubscriptionOperation(ctx, op.Method, op.ID)
		if err != nil {
			return err
		}
		if existing.TelegramID != op.TelegramID || existing.SubscriptionID != op.SubscriptionID {
			return ErrSubscriptionOwner
		}
	}
	return nil
}

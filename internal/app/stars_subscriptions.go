package app

import (
	"context"
	"crypto/rand"
	"errors"
	"strconv"
	"strings"
	"time"

	"remnabot/internal/model"
	"remnabot/internal/storage"
)

func (a *App) subscriptionStarsPayload(ctx context.Context, owner int64, s *sale, amount int, legacy string) (string, error) {
	if !a.multiSubscriptions() && s.SubscriptionID == "" {
		return legacy, nil
	}
	st, ok := a.storeRef().(storage.SubscriptionOperationStorage)
	if !ok || owner <= 0 {
		return "", errors.New("для оплаты Stars привяжите Telegram к своему аккаунту")
	}
	snap := a.saleSnapshot(s)
	snap.InvoiceID = rand.Text()
	op := &model.SubscriptionOperation{Method: model.PayMethodStars, ID: snap.InvoiceID, TelegramID: owner,
		SubscriptionID: snap.SubscriptionID, Amount: strconv.Itoa(amount) + " ⭐", Snapshot: snap}
	if err := st.IssueSubscriptionInvoice(ctx, op); err != nil {
		return "", err
	}
	return "stars:" + strconv.Itoa(s.Months) + ":" + strconv.FormatInt(owner, 10) + ":" + snap.InvoiceID, nil
}

// A Stars invoice is keyed by its unique payload, not by a mutable combination
// of customer and duration. Two invoices for two accounts cannot replace each other.
func (a *App) starsSnapshotForPayload(ctx context.Context, owner int64, payload string, amount int, checkout bool) (*model.PlanSnapshot, bool) {
	parts := strings.Split(payload, ":")
	months, _ := starsPayload(payload)
	if months <= 0 {
		return nil, false
	}
	if len(parts) <= 3 {
		if a.multiSubscriptions() {
			st, err := a.subscriptionStore()
			if err != nil {
				return nil, false
			}
			bindings, err := st.CustomerSubscriptions(ctx, owner)
			if err != nil || len(bindings) > 1 {
				return nil, false
			}
		}
		snap, ok := a.starsSnapshotForAmount(ctx, owner, months, amount)
		if ok {
			_, _, err := a.dealSubscriptionContext(ctx, owner, snap)
			return snap, err == nil
		}
		return nil, false
	}
	if len(parts) != 4 || parts[3] == "" {
		return nil, false
	}
	st, ok := a.storeRef().(storage.SubscriptionOperationStorage)
	if !ok {
		return nil, false
	}
	op, err := st.SubscriptionOperation(ctx, model.PayMethodStars, parts[3])
	if err != nil || op == nil || op.TelegramID != owner || op.Amount != strconv.Itoa(amount)+" ⭐" ||
		op.Snapshot == nil || op.Snapshot.InvoiceID != parts[3] || op.Snapshot.Months != months {
		return nil, false
	}
	if checkout {
		created, err := time.Parse(time.RFC3339, op.CreatedAt)
		if err != nil || op.Phase != "issued" || time.Since(created) > 24*time.Hour {
			return nil, false
		}
	}
	if _, _, err := a.dealSubscriptionContext(ctx, owner, op.Snapshot); err != nil {
		return nil, false
	}
	return op.Snapshot, true
}

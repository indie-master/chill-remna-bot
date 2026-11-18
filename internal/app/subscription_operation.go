package app

import (
	"context"
	"errors"

	"remnabot/internal/model"
	"remnabot/internal/storage"
)

func (a *App) claimDealOperation(ctx context.Context, owner int64, method, extID, amount string, snap *model.PlanSnapshot) (storage.SubscriptionOperationStorage, string, error) {
	if !a.multiSubscriptions() && snap.SubscriptionID == "" && snap.InvoiceID == "" {
		return nil, "", nil
	}
	st, ok := a.storeRef().(storage.SubscriptionOperationStorage)
	if !ok || extID == "" {
		return nil, "", errors.New("платёж нельзя безопасно сопоставить с подпиской")
	}
	key := extID
	if snap.InvoiceID != "" {
		key = snap.InvoiceID
	}
	op := &model.SubscriptionOperation{Method: method, ID: key, TelegramID: owner,
		SubscriptionID: snap.SubscriptionID, Amount: amount, Snapshot: snap, PaymentRef: extID}
	if err := st.ClaimSubscriptionOperation(ctx, op); err != nil {
		return nil, "", err
	}
	return st, key, nil
}

package app

import (
	"context"
	"errors"

	"remnabot/internal/model"
	"remnabot/internal/remnawave"
	"remnabot/internal/storage"
)

var errSelectSubscription = errors.New("сначала выберите подписку в разделе «Мои подписки»")

func (a *App) tributeSubscriptionSupported(ctx context.Context, owner int64) bool {
	if !a.multiSubscriptions() {
		return true
	}
	st, err := a.subscriptionStore()
	if err != nil {
		return false
	}
	all, err := st.CustomerSubscriptions(ctx, owner)
	return err == nil && len(all) <= 1
}

func (a *App) subscriptionUser(ctx context.Context, owner int64) (*model.User, error) {
	st := a.storeRef()
	if st == nil {
		return nil, nil
	}
	u, err := st.GetUser(ctx, owner)
	if err != nil || u == nil || (!a.multiSubscriptions() && remnawave.SubscriptionTargetID(ctx, owner) == "") {
		return u, err
	}
	sub, err := a.selectedSubscription(ctx, owner)
	if err != nil || sub == nil {
		return u, err
	}
	copy := *u
	copy.SubExpireAt = sub.ExpireAt
	copy.Snapshot = sub.Snapshot
	copy.NotifyKind = sub.Kind
	return &copy, nil
}

func (a *App) saleSubscriptionID(ctx context.Context, owner int64) (string, error) {
	if !a.multiSubscriptions() {
		return "", nil
	}
	if id := remnawave.SubscriptionTargetID(ctx, owner); id != "" {
		if _, err := a.resolveSubscriptionRef(ctx, owner, id); err != nil {
			return "", err
		}
		return id, nil
	}
	if _, err := a.discoverSubscriptions(ctx, owner); err != nil {
		return "", err
	}
	s, err := a.selectedSubscription(ctx, owner)
	if s == nil || err != nil {
		return "", err
	}
	return s.ID, nil
}

func (a *App) multiSubscriptions() bool {
	return a.cfg != nil && a.cfg.MultiSubscriptions
}

func (a *App) subscriptionStore() (storage.SubscriptionStorage, error) {
	st, ok := a.storeRef().(storage.SubscriptionStorage)
	if !ok {
		return nil, errors.New("хранилище подписок недоступно")
	}
	return st, nil
}

func (a *App) selectedSubscription(ctx context.Context, owner int64) (*model.CustomerSubscription, error) {
	st, err := a.subscriptionStore()
	if err != nil {
		return nil, err
	}
	if id := remnawave.SubscriptionTargetID(ctx, owner); id != "" {
		s, err := st.CustomerSubscription(ctx, owner, id)
		if err == nil && s == nil {
			err = storage.ErrSubscriptionOwner
		}
		return s, err
	}
	s, err := st.SelectedSubscription(ctx, owner)
	if err != nil || s != nil {
		return s, err
	}
	all, err := st.CustomerSubscriptions(ctx, owner)
	if err != nil {
		return nil, err
	}
	if len(all) > 1 {
		return nil, errSelectSubscription
	}
	if len(all) == 1 {
		return &all[0], nil
	}
	return nil, nil
}

func (a *App) resolveSubscriptionRef(ctx context.Context, owner int64, id string) (string, error) {
	if id == "" && !a.multiSubscriptions() {
		return "", nil
	}
	st, err := a.subscriptionStore()
	if err != nil {
		return "", err
	}
	var s *model.CustomerSubscription
	if id != "" {
		s, err = st.CustomerSubscription(ctx, owner, id)
		if err == nil && s == nil {
			err = storage.ErrSubscriptionOwner
		}
	} else {
		s, err = a.selectedSubscription(ctx, owner)
	}
	if err != nil || s == nil {
		return "", err
	}
	return s.PanelRef, nil
}

func (a *App) dealSubscriptionContext(ctx context.Context, owner int64, snap *model.PlanSnapshot) (context.Context, *model.CustomerSubscription, error) {
	if snap != nil && snap.SubscriptionID != "" {
		st, err := a.subscriptionStore()
		if err != nil {
			return ctx, nil, err
		}
		s, err := st.CustomerSubscription(ctx, owner, snap.SubscriptionID)
		if err != nil {
			return ctx, nil, err
		}
		if s == nil {
			return ctx, nil, storage.ErrSubscriptionOwner
		}
		return remnawave.WithSubscriptionTarget(ctx, owner, s.ID), s, nil
	}
	if st, ok := a.storeRef().(storage.SubscriptionStorage); ok {
		all, err := st.CustomerSubscriptions(ctx, owner)
		if err != nil {
			return ctx, nil, err
		}
		if len(all) > 1 {
			return ctx, nil, errSelectSubscription
		}
		if len(all) == 1 {
			if snap != nil && snap.NewAccount {
				return ctx, nil, errSelectSubscription
			}
			return remnawave.WithSubscriptionTarget(ctx, owner, all[0].ID), &all[0], nil
		}
	} else if a.multiSubscriptions() {
		return ctx, nil, errors.New("хранилище подписок недоступно")
	}
	return ctx, nil, nil
}

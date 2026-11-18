package app

import (
	"context"
	"errors"

	"remnabot/internal/remnawave"

	"remnabot/internal/web"
)

func (a *App) MiniSubscriptionScope(ctx context.Context, owner int64, id string) (context.Context, error) {
	if !a.multiSubscriptions() {
		return ctx, errors.New("выбор подписки выключен")
	}
	st, err := a.subscriptionStore()
	if err != nil {
		return ctx, err
	}
	sub, err := st.CustomerSubscription(ctx, owner, id)
	if err != nil || sub == nil {
		return ctx, errors.New("подписка недоступна")
	}
	return remnawave.WithSubscriptionTarget(ctx, owner, id), nil
}

func (a *App) MiniSubscriptions(ctx context.Context, owner int64) web.MiniSubscriptionsDTO {
	dto := web.MiniSubscriptionsDTO{Enabled: a.multiSubscriptions(), Items: []web.MiniSubscriptionItemDTO{}}
	if !dto.Enabled {
		return dto
	}
	subs, err := a.discoverSubscriptions(ctx, owner)
	if err != nil {
		dto.Error = "Не удалось обновить список подписок"
		return dto
	}
	st, err := a.subscriptionStore()
	if err != nil {
		dto.Error = "Не удалось получить список подписок"
		return dto
	}
	selected, err := st.SelectedSubscription(ctx, owner)
	if err != nil {
		dto.Error = "Не удалось получить выбранную подписку"
		return dto
	}
	if selected != nil {
		dto.SelectedID = selected.ID
	} else if len(subs) == 1 {
		dto.SelectedID = subs[0].ID
	}
	for _, s := range subs {
		dto.Items = append(dto.Items, web.MiniSubscriptionItemDTO{ID: s.ID, Label: s.Label, ExpireAt: s.ExpireAt})
	}
	return dto
}

func (a *App) MiniSelectSubscription(ctx context.Context, owner int64, id string) web.MiniActionDTO {
	if !a.multiSubscriptions() {
		return web.MiniActionDTO{Error: "Выбор нескольких подписок выключен"}
	}
	st, err := a.subscriptionStore()
	if err == nil {
		err = st.SelectSubscription(ctx, owner, id)
	}
	if err != nil {
		return web.MiniActionDTO{Error: "Подписка недоступна. Обновите список"}
	}
	return web.MiniActionDTO{OK: true}
}

package app

import (
	"context"
	"strings"

	"github.com/go-telegram/bot/models"
	"remnabot/internal/remnawave"
	"remnabot/internal/storage"
)

func (a *App) subscriptionButtons(ctx context.Context, owner int64, rows [][]models.InlineKeyboardButton) [][]models.InlineKeyboardButton {
	id := remnawave.SubscriptionTargetID(ctx, owner)
	if id == "" {
		return rows
	}
	st, ok := a.storeRef().(storage.SubscriptionCallbackStorage)
	if !ok {
		return [][]models.InlineKeyboardButton{{btn("Мои подписки", "subpick:list")}}
	}
	out := make([][]models.InlineKeyboardButton, len(rows))
	for i, row := range rows {
		out[i] = append([]models.InlineKeyboardButton(nil), row...)
		for j, button := range row {
			data := button.CallbackData
			if data == "" || data == "menu:home" || strings.HasPrefix(data, "subpick:") || strings.HasPrefix(data, "cs:") || !clientData(data) {
				continue
			}
			token, err := st.SubscriptionCallbackToken(ctx, owner, id, data)
			if err != nil {
				return [][]models.InlineKeyboardButton{{btn("Обновить список подписок", "subpick:list")}}
			}
			out[i][j].CallbackData = "cs:" + token
		}
	}
	return out
}

func (a *App) subscriptionCallbackContext(ctx context.Context, cq *models.CallbackQuery) (context.Context, *models.CallbackQuery, bool) {
	if !strings.HasPrefix(cq.Data, "cs:") {
		return ctx, cq, true
	}
	st, ok := a.storeRef().(storage.SubscriptionCallbackStorage)
	if ok {
		cb, err := st.SubscriptionCallback(ctx, cq.From.ID, strings.TrimPrefix(cq.Data, "cs:"))
		if err == nil && cb != nil {
			bindings, err := a.subscriptionStore()
			if err == nil {
				s, err := bindings.CustomerSubscription(ctx, cq.From.ID, cb.SubscriptionID)
				if err == nil && s != nil {
					copy := *cq
					copy.Data = cb.Data
					ctx = remnawave.WithSubscriptionTarget(ctx, cq.From.ID, cb.SubscriptionID)
					return ctx, &copy, true
				}
			}
		}
	}
	a.msg.AnswerCallbackText(ctx, cq.ID, "Меню устарело. Откройте список подписок ещё раз")
	return ctx, cq, false
}

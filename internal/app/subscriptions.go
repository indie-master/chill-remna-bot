package app

import (
	"context"
	"errors"
	"html"
	"time"

	"github.com/go-telegram/bot/models"
	"remnabot/internal/model"
	"remnabot/internal/storage"
)

func (a *App) discoverSubscriptions(ctx context.Context, owner int64) ([]model.CustomerSubscription, error) {
	st, err := a.subscriptionStore()
	if err != nil {
		return nil, err
	}
	known, err := st.CustomerSubscriptions(ctx, owner)
	if err != nil || owner < 0 {
		if err == nil && len(known) > 0 {
			if u, readErr := a.storeRef().GetUser(ctx, owner); readErr != nil {
				return nil, readErr
			} else if u != nil && u.TrialUsedAt == "" {
				if err := a.storeRef().SetTrialUsed(ctx, owner, time.Now().UTC().Format(time.RFC3339)); err != nil {
					return nil, err
				}
			}
		}
		return known, err
	}
	panel := a.panelClient()
	if panel == nil {
		return nil, errors.New("панель недоступна")
	}
	accounts, err := panel.CustomerAccounts(ctx, owner)
	if err != nil {
		return nil, err
	}
	byRef := map[string]model.CustomerSubscription{}
	for _, s := range known {
		byRef[s.PanelRef] = s
	}
	for _, account := range accounts {
		if s, found := byRef[account.Ref.Key()]; found {
			if err := st.RefreshSubscriptionExpiry(ctx, &s, account.ExpireAt); err != nil {
				return nil, err
			}
			continue
		}
		label := account.Username
		if label == "" {
			label = "Подписка " + account.Ref.Key()
		}
		s := &model.CustomerSubscription{TelegramID: owner, PanelRef: account.Ref.Key(), Label: label, ExpireAt: account.ExpireAt}
		var previous *model.User
		if len(accounts) == 1 && len(known) == 0 {
			previous, err = a.storeRef().GetUser(ctx, owner)
			if err != nil {
				return nil, err
			}
			if previous != nil && (previous.NotifyKind == "trial" || previous.NotifyKind == "paid") {
				s.Kind = previous.NotifyKind
			}
		}
		if err := st.BindSubscription(ctx, s); err != nil {
			// A concurrent first /start may have registered the same ownership.
			if !errors.Is(err, storage.ErrSubscriptionBound) {
				return nil, err
			}
			current, readErr := st.CustomerSubscriptions(ctx, owner)
			if readErr != nil {
				return nil, readErr
			}
			found := false
			for _, item := range current {
				if item.PanelRef == s.PanelRef {
					*s = item
					found = true
					break
				}
			}
			if !found {
				return nil, storage.ErrSubscriptionOwner
			}
		}
		if previous != nil && previous.Snapshot != nil {
			copy := *previous.Snapshot.ForRenewal()
			copy.SubscriptionID = s.ID
			if err := st.SetSubscriptionDeal(ctx, owner, s.ID, account.ExpireAt, &copy); err != nil {
				return nil, err
			}
		}
	}
	result, err := st.CustomerSubscriptions(ctx, owner)
	if err == nil && len(result) == 1 {
		ap, readErr := a.storeRef().GetAutoPay(ctx, owner)
		if readErr != nil {
			return nil, readErr
		}
		if ap != nil && ap.Snapshot != nil && ap.Snapshot.SubscriptionID == "" {
			snap := ap.Snapshot.ForRenewal()
			snap.SubscriptionID = result[0].ID
			if err := a.storeRef().UpdateAutoPaySnapshot(ctx, owner, snap); err != nil {
				return nil, err
			}
		}
	}
	if err == nil && len(result) > 0 {
		// Owning an existing account, including an expired one, is not a new trial.
		if u, readErr := a.storeRef().GetUser(ctx, owner); readErr != nil {
			return nil, readErr
		} else if u == nil || u.TrialUsedAt == "" {
			if u == nil {
				if err := a.storeRef().UpsertUser(ctx, owner); err != nil {
					return nil, err
				}
			}
			if err := a.storeRef().SetTrialUsed(ctx, owner, time.Now().UTC().Format(time.RFC3339)); err != nil {
				return nil, err
			}
		}
	}
	return result, err
}

func (a *App) showSubscriptions(ctx context.Context, owner int64) {
	subs, err := a.discoverSubscriptions(ctx, owner)
	if err != nil {
		a.sendKB(ctx, owner, "Не удалось получить список подписок. Попробуйте обновить его.", [][]models.InlineKeyboardButton{
			{btn("Обновить", "subpick:list")}, {btn("На главную", "menu:home")},
		})
		return
	}
	var rows [][]models.InlineKeyboardButton
	for _, s := range subs {
		rows = append(rows, []models.InlineKeyboardButton{btn(s.Label, "subpick:"+s.ID)})
	}
	text := "<b>Мои подписки</b>\nВыберите подключение. У каждой подписки свой срок и условия."
	if len(subs) == 0 {
		text = "У вас пока нет подписок."
		rows = append(rows, []models.InlineKeyboardButton{btn("Выбрать тариф", "menu:buy")})
	}
	rows = append(rows, []models.InlineKeyboardButton{btn("На главную", "menu:home")})
	a.sendKB(ctx, owner, text, rows)
}

func (a *App) onSubscriptionPick(ctx context.Context, owner int64, id string) {
	if !a.multiSubscriptions() {
		return
	}
	if id == "list" {
		a.showSubscriptions(ctx, owner)
		return
	}
	st, err := a.subscriptionStore()
	if err == nil {
		err = st.SelectSubscription(ctx, owner, id)
	}
	if err != nil {
		a.sendKB(ctx, owner, "Подписка недоступна. Обновите список.", [][]models.InlineKeyboardButton{{btn("Мои подписки", "subpick:list")}})
		return
	}
	a.showMySubs(ctx, owner)
}

func subscriptionLabel(s *model.CustomerSubscription) string {
	if s == nil {
		return ""
	}
	return "<b>" + html.EscapeString(s.Label) + "</b>\n"
}

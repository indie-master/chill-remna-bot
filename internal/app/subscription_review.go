package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"html"
	"time"

	"github.com/go-telegram/bot/models"
	"remnabot/internal/model"
	"remnabot/internal/remnawave"
	"remnabot/internal/storage"
)

type subscriptionReview struct {
	method, id, action string
	expires            time.Time
}

func (a *App) reviewToken(op model.SubscriptionOperation, action string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.subscriptionReviews == nil {
		a.subscriptionReviews = map[string]subscriptionReview{}
	}
	for token, review := range a.subscriptionReviews {
		if time.Now().After(review.expires) {
			delete(a.subscriptionReviews, token)
		}
	}
	token := rand.Text()
	a.subscriptionReviews[token] = subscriptionReview{op.Method, op.ID, action, time.Now().Add(3 * time.Minute)}
	return "sop:" + token
}

func (a *App) reviewFor(actor int64, token string) (subscriptionReview, bool) {
	if a.cfg == nil || actor == 0 || actor != a.cfg.AdminID {
		return subscriptionReview{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.subscriptionReviews[token]
	if !ok || time.Now().After(r.expires) {
		return subscriptionReview{}, false
	}
	if r.action == "apply" || r.action == "cancel" {
		delete(a.subscriptionReviews, token)
	}
	return r, true
}

func (a *App) reconcileSubscription(ctx context.Context, op *model.SubscriptionOperation) error {
	if op == nil || op.Phase != "executing" || op.SubscriptionID == "" || op.Snapshot == nil {
		return storage.ErrSubscriptionData
	}
	expected, err := time.Parse(time.RFC3339, op.ExpectedExpiry)
	if err != nil {
		return storage.ErrSubscriptionData
	}
	panel := a.panelClient()
	if panel == nil {
		return errors.New("панель недоступна")
	}
	ctx = remnawave.WithSubscriptionTarget(ctx, op.TelegramID, op.SubscriptionID)
	_, expiry, _, found, err := panel.SubscriptionState(ctx, op.TelegramID)
	if err != nil {
		return err
	}
	actual, parseErr := time.Parse(time.RFC3339, expiry)
	if !found || parseErr != nil || !expected.Equal(actual) {
		return errors.New("срок в панели отличается от сохранённого результата; требуется ручная сверка")
	}
	st := a.storeRef().(storage.SubscriptionOperationStorage)
	err = st.CommitSubscriptionOperation(ctx, op.ID, &model.Payment{TelegramID: op.TelegramID, Method: op.Method,
		Months: op.Snapshot.Months, Amount: op.Amount, Status: model.PaymentPaid, ExtID: op.PaymentRef, Snapshot: op.Snapshot}, expiry)
	if err == nil {
		a.invalidateSubCache(op.TelegramID)
	}
	return err
}

func (a *App) onSubscriptionReview(ctx context.Context, actor int64, val string) {
	if !a.multiSubscriptions() || a.cfg == nil || actor == 0 || actor != a.cfg.AdminID {
		return
	}
	st, ok := a.storeRef().(storage.SubscriptionOperationStorage)
	if !ok {
		return
	}
	back := []models.InlineKeyboardButton{btn("Назад к сверке", "sop:list"), btn("Главное меню", "menu:home")}
	if val == "list" {
		ops, err := st.PendingSubscriptionOperations(ctx)
		if err != nil {
			a.sendKB(ctx, actor, "Не удалось прочитать журнал операций.", [][]models.InlineKeyboardButton{back})
			return
		}
		rows := make([][]models.InlineKeyboardButton, 0, len(ops)+1)
		for _, op := range ops {
			rows = append(rows, []models.InlineKeyboardButton{btn(fmt.Sprintf("%d · %s · %s", op.TelegramID, op.Method, op.Amount), a.reviewToken(op, "view"))})
		}
		rows = append(rows, []models.InlineKeyboardButton{btn("Главное меню", "menu:home")})
		a.sendKB(ctx, actor, fmt.Sprintf("<b>Сверка выдачи подписок</b>\nОжидают проверки: %d (первые 50).\nПовторной отправки изменений в панель нет. Подтверждения действуют 3 минуты.", len(ops)), rows)
		return
	}
	review, ok := a.reviewFor(actor, val)
	if !ok {
		a.sendKB(ctx, actor, "Подтверждение истекло или уже использовано. Откройте сверку заново.", [][]models.InlineKeyboardButton{back})
		return
	}
	op, err := st.SubscriptionOperation(ctx, review.method, review.id)
	if err != nil || op == nil || op.Phase != "executing" {
		a.sendKB(ctx, actor, "Операция уже закрыта или недоступна.", [][]models.InlineKeyboardButton{back})
		return
	}
	switch review.action {
	case "view":
		text := fmt.Sprintf("<b>Проверка выдачи</b>\nКлиент: %d\nПодключение: %s\nСпособ: %s\nСумма: %s\nОжидаемый срок: %s\n\n«Выдача состоялась» сверяет срок через API и только записывает платёж.\nПеред отменой проверьте, что услуга не выдана, либо вручную восстановите прежние условия в панели.", op.TelegramID, html.EscapeString(op.SubscriptionID), html.EscapeString(op.Method), html.EscapeString(op.Amount), html.EscapeString(op.ExpectedExpiry))
		cancel := "Возврат во внешней системе уже выполнен. Подтвердить отмену?"
		if op.Method == "balance" {
			cancel = "При подтверждении зарезервированная сумма вернётся на баланс клиента один раз."
		}
		rows := [][]models.InlineKeyboardButton{}
		if op.SubscriptionID != "" && op.ExpectedExpiry != "" {
			rows = append(rows, []models.InlineKeyboardButton{btn("Выдача состоялась", a.reviewToken(*op, "askApply"))})
		}
		rows = append(rows, []models.InlineKeyboardButton{btn("Отменить после сверки", a.reviewToken(*op, "askCancel"))}, back)
		a.sendKB(ctx, actor, text+"\n\n"+cancel, rows)
	case "askApply", "askCancel":
		action, question := "apply", "Подтвердить уже выданную услугу? Срок будет повторно проверен через API."
		if review.action == "askCancel" {
			action, question = "cancel", "Подтверждаю: услуга не выдана или исправлена в панели, внешний платёж возвращён. Закрыть операцию?"
			if op.Method == "balance" {
				question = "Подтверждаю: услуга не выдана или исправлена в панели. Закрыть операцию и вернуть резерв на баланс?"
			}
		}
		a.sendKB(ctx, actor, question, [][]models.InlineKeyboardButton{{btn("Подтвердить", a.reviewToken(*op, action))}, back})
	case "apply", "cancel":
		if review.action == "apply" {
			err = a.reconcileSubscription(ctx, op)
		} else {
			err = st.CancelSubscriptionOperation(ctx, op.Method, op.ID)
		}
		text := "Операция закрыта. Повторное нажатие не меняет панель и не повторяет возврат."
		if err != nil {
			text = "Результат не подтверждён. Проверьте доступность панели, фактический срок и состояние операции. Операция сохранена для сверки."
		}
		a.sendKB(ctx, actor, text, [][]models.InlineKeyboardButton{back})
	}
}

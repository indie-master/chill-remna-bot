package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"remnabot/internal/crypto"
	"remnabot/internal/model"
	"remnabot/internal/remnawave"
	"remnabot/internal/storage"
	_ "remnabot/internal/storage/drivers"
	"remnabot/internal/yookassa"
)

type subscriptionPanel struct {
	mu             sync.Mutex
	expiry         map[string]string
	writes         []string
	failAfterWrite bool
	connectBase    string
}

func (p *subscriptionPanel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	user := func(ref string) map[string]any {
		base := p.connectBase
		if base == "" {
			base = "https://subscriptions.example.invalid"
		}
		return map[string]any{"uuid": ref, "username": ref, "telegramId": 10001, "status": "ACTIVE",
			"expireAt": p.expiry[ref], "hwidDeviceLimit": 3, "trafficLimitStrategy": "DAY",
			"subscriptionUrl": base + "/" + ref}
	}
	respond := func(v any) { _ = json.NewEncoder(w).Encode(map[string]any{"response": v}) }
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/users/by-telegram-id/10001":
		respond([]any{user("panel-a"), user("panel-b")})
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/users/panel-"):
		respond(user(strings.TrimPrefix(r.URL.Path, "/api/users/")))
	case r.Method == http.MethodPatch && r.URL.Path == "/api/users":
		var body struct {
			UUID     string `json:"uuid"`
			ExpireAt string `json:"expireAt"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || p.expiry[body.UUID] == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		p.expiry[body.UUID] = body.ExpireAt
		p.writes = append(p.writes, body.UUID)
		if p.failAfterWrite {
			p.failAfterWrite = false
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		respond(user(body.UUID))
	case r.Method == http.MethodGet:
		respond([]any{})
	default:
		w.WriteHeader(http.StatusBadRequest)
	}
}

func newSubscriptionApp(t *testing.T) (*App, *subscriptionPanel) {
	t.Helper()
	panel := &subscriptionPanel{expiry: map[string]string{"panel-a": "2027-01-01T09:00:00Z", "panel-b": "2027-03-01T09:00:00Z"}}
	server := httptest.NewServer(panel)
	t.Cleanup(server.Close)
	a, _, _ := grantApp(t, server.URL)
	a.cfg.MultiSubscriptions = true
	crypter, err := crypto.NewFromKeyMaterial([]byte("synthetic-test-key"))
	if err != nil {
		t.Fatal(err)
	}
	st, err := storage.Open(model.DBSQLite, filepath.Join(t.TempDir(), "test.db"), crypter)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	a.store = st
	a.panel = a.newPanel(model.PanelConfig{Mode: model.ModeRemote, BaseURL: server.URL, APIToken: "synthetic"})
	if err := st.UpsertUser(context.Background(), 10001); err != nil {
		t.Fatal(err)
	}
	return a, panel
}

func TestMiniConnectFallsBackForEachSelectedSubscription(t *testing.T) {
	a, panel := newSubscriptionApp(t)
	page := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(page.Close)
	panel.mu.Lock()
	panel.connectBase = page.URL
	panel.mu.Unlock()
	ctx := context.Background()
	subs, err := a.discoverSubscriptions(ctx, 10001)
	if err != nil || len(subs) != 2 {
		t.Fatalf("expected two panel accounts: %v", err)
	}
	for _, sub := range subs {
		scoped, err := a.MiniSubscriptionScope(ctx, 10001, sub.ID)
		if err != nil {
			t.Fatal(err)
		}
		got := a.MiniConnect(scoped, 10001)
		want := page.URL + "/" + sub.PanelRef
		if got.SubURL != want || len(got.Android) != 2 || len(got.IOS) != 2 {
			t.Fatalf("wrong scoped app catalog: %+v, want %s", got, want)
		}
		for _, app := range append(got.IOS, got.Android...) {
			if app.Deeplink != strings.ToLower(app.Name)+"://add/"+want {
				t.Fatalf("%s imported a different subscription: %q", app.Name, app.Deeplink)
			}
		}
	}
}

func TestSubscriptionPaymentFollowsInvoiceAcrossSelectionChanges(t *testing.T) {
	a, panel := newSubscriptionApp(t)
	ctx := context.Background()
	subs, err := a.discoverSubscriptions(ctx, 10001)
	if err != nil || len(subs) != 2 {
		t.Fatalf("discovery: %v %v", subs, err)
	}
	bindings := a.store.(storage.SubscriptionStorage)
	if err := bindings.SelectSubscription(ctx, 10001, subs[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := a.setBuyIntent(ctx, 10001, model.PlanCodeBase, 1, "150"); err != nil {
		t.Fatal(err)
	}
	sale, err := a.saleFor(ctx, 10001)
	if err != nil || sale == nil {
		t.Fatalf("sale: %v %v", sale, err)
	}
	snap := a.saleSnapshot(sale)
	if err := bindings.SelectSubscription(ctx, 10001, subs[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.finalizePurchase(ctx, 10001, 1, "test", "150 ₽", "invoice-a", snap); err != nil {
		t.Fatal(err)
	}
	if len(panel.writes) != 1 || panel.writes[0] != "panel-a" || panel.expiry["panel-b"] != "2027-03-01T09:00:00Z" {
		t.Fatalf("payment modified the wrong account: %v", panel.writes)
	}
	if _, _, err := a.finalizePurchase(ctx, 10001, 1, "test", "150 ₽", "invoice-a", snap); !errors.Is(err, storage.ErrDuplicateExtID) || len(panel.writes) != 1 {
		t.Fatal("duplicate notification extended again")
	}
	webCtx, err := a.MiniSubscriptionScope(ctx, 10001, subs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if s := a.miniSale(webCtx, 10001, model.PlanCodeBase, 1); s == nil || s.SubscriptionID != subs[0].ID {
		t.Fatal("another browser tab changed checkout target")
	}
	if _, err := a.MiniSubscriptionScope(ctx, 10002, subs[0].ID); err == nil {
		t.Fatal("foreign subscription accepted")
	}
}

func TestSubscriptionUncertainWriteIsNeverRepeated(t *testing.T) {
	a, panel := newSubscriptionApp(t)
	ctx := context.Background()
	subs, err := a.discoverSubscriptions(ctx, 10001)
	if err != nil {
		t.Fatal(err)
	}
	panel.failAfterWrite = true
	snap := &model.PlanSnapshot{SubscriptionID: subs[0].ID, Code: model.PlanCodeBase, Months: 1, Strategy: "DAY"}
	if _, _, err := a.finalizePurchase(ctx, 10001, 1, "test", "150 ₽", "uncertain-a", snap); !errors.Is(err, storage.ErrSubscriptionUncertain) {
		t.Fatalf("uncertain result not held: %v", err)
	}
	if _, _, err := a.finalizePurchase(ctx, 10001, 1, "test", "150 ₽", "uncertain-a", snap); !errors.Is(err, storage.ErrSubscriptionHeld) || len(panel.writes) != 1 {
		t.Fatal("uncertain write was repeated")
	}
}

func TestSubscriptionRecoveryVerifiesPanelWithoutRepeatingMutation(t *testing.T) {
	a, panel := newSubscriptionApp(t)
	ctx := context.Background()
	subs, err := a.discoverSubscriptions(ctx, 10001)
	if err != nil {
		t.Fatal(err)
	}
	panel.failAfterWrite = true
	snap := &model.PlanSnapshot{SubscriptionID: subs[0].ID, Months: 1, Strategy: "DAY"}
	if _, _, err := a.finalizePurchase(ctx, 10001, 1, "test", "150", "recover-a", snap); !errors.Is(err, storage.ErrSubscriptionUncertain) {
		t.Fatal(err)
	}
	st := a.store.(storage.SubscriptionOperationStorage)
	op, err := st.SubscriptionOperation(ctx, "test", "recover-a")
	if err != nil || op == nil || op.ExpectedExpiry == "" {
		t.Fatalf("outbound result not persisted: %+v %v", op, err)
	}
	expected := panel.expiry["panel-a"]
	panel.expiry["panel-a"] = "2027-01-01T00:00:00Z"
	if err := a.reconcileSubscription(ctx, op); err == nil {
		t.Fatal("different expiry was accepted")
	}
	panel.expiry["panel-a"] = expected
	if err := a.store.(storage.SubscriptionStorage).SelectSubscription(ctx, 10001, subs[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := a.reconcileSubscription(ctx, op); err != nil {
		t.Fatal(err)
	}
	if len(panel.writes) != 1 {
		t.Fatal("reconciliation mutated the panel")
	}
	if pending, err := st.PendingSubscriptionOperations(ctx); err != nil || len(pending) != 0 {
		t.Fatal("completed operation still pending")
	}
	got, _ := a.store.(storage.SubscriptionStorage).CustomerSubscription(ctx, 10001, subs[0].ID)
	if got.ExpireAt != expected {
		t.Fatal("reconciliation did not update its fixed account")
	}
	if err := st.CancelSubscriptionOperation(ctx, op.Method, op.ID); err == nil {
		t.Fatal("paid operation was cancelled")
	}

	token := strings.TrimPrefix(a.reviewToken(*op, "apply"), "sop:")
	if _, ok := a.reviewFor(a.cfg.AdminID+1, token); ok {
		t.Fatal("foreign actor accepted")
	}
	if _, ok := a.reviewFor(a.cfg.AdminID, token); !ok {
		t.Fatal("admin confirmation rejected")
	}
	if _, ok := a.reviewFor(a.cfg.AdminID, token); ok {
		t.Fatal("confirmation replay accepted")
	}
	token = strings.TrimPrefix(a.reviewToken(*op, "cancel"), "sop:")
	r := a.subscriptionReviews[token]
	r.expires = time.Now().Add(-time.Second)
	a.subscriptionReviews[token] = r
	if _, ok := a.reviewFor(a.cfg.AdminID, token); ok {
		t.Fatal("expired confirmation accepted")
	}
}

func TestSubscriptionButtonsKeepTheirOriginalAccount(t *testing.T) {
	a, _ := newSubscriptionApp(t)
	ctx := context.Background()
	subs, err := a.discoverSubscriptions(ctx, 10001)
	if err != nil {
		t.Fatal(err)
	}
	ctx = remnawave.WithSubscriptionTarget(ctx, 10001, subs[0].ID)
	rows := a.subscriptionButtons(ctx, 10001, [][]models.InlineKeyboardButton{{btn("Renew", "menu:renew")}})
	if len(rows[0][0].CallbackData) > 64 || !strings.HasPrefix(rows[0][0].CallbackData, "cs:") {
		t.Fatal("invalid scoped callback")
	}
	if err := a.store.(storage.SubscriptionStorage).SelectSubscription(ctx, 10001, subs[1].ID); err != nil {
		t.Fatal(err)
	}
	cq := &models.CallbackQuery{From: models.User{ID: 10001}, Data: rows[0][0].CallbackData}
	bound, restored, ok := a.subscriptionCallbackContext(context.Background(), cq)
	if !ok || restored.Data != "menu:renew" || remnawave.SubscriptionTargetID(bound, 10001) != subs[0].ID {
		t.Fatal("old card followed the new global selection")
	}
}

func TestSubscriptionStarsInvoicesAreIndependentForTheSameDuration(t *testing.T) {
	a, panel := newSubscriptionApp(t)
	a.botCfg.Stars.Enabled = true
	ctx := context.Background()
	subs, err := a.discoverSubscriptions(ctx, 10001)
	if err != nil {
		t.Fatal(err)
	}
	sa, sb := baseSale(1), baseSale(1)
	sa.SubscriptionID, sb.SubscriptionID = subs[0].ID, subs[1].ID
	pa, err := a.subscriptionStarsPayload(ctx, 10001, sa, 50, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	pb, err := a.subscriptionStarsPayload(ctx, 10001, sb, 50, "legacy")
	if err != nil || pa == pb {
		t.Fatal("two invoices shared their identity")
	}
	if _, ok := a.starsSnapshotForPayload(ctx, 10002, pa, 50, true); ok {
		t.Fatal("invoice accepted a foreign payer")
	}
	if _, ok := a.starsSnapshotForPayload(ctx, 10001, pa, 51, true); ok {
		t.Fatal("invoice accepted a different amount")
	}
	if err := a.store.(storage.SubscriptionStorage).SelectSubscription(ctx, 10001, subs[1].ID); err != nil {
		t.Fatal(err)
	}
	snap, ok := a.starsSnapshotForPayload(ctx, 10001, pa, 50, true)
	if !ok || snap.SubscriptionID != subs[0].ID {
		t.Fatal("first invoice was replaced by the second")
	}
	if _, _, err := a.finalizePurchase(ctx, 10001, 1, model.PayMethodStars, "50 ⭐", "star-charge-a", snap); err != nil {
		t.Fatal(err)
	}
	if len(panel.writes) != 1 || panel.writes[0] != "panel-a" {
		t.Fatal("Stars renewed the wrong subscription")
	}
	if _, ok := a.starsSnapshotForPayload(ctx, 10001, pa, 50, true); ok {
		t.Fatal("completed invoice offered another payment")
	}
	if pending, ok := a.starsSnapshotForPayload(ctx, 10001, pb, 50, true); !ok || pending.SubscriptionID != subs[1].ID {
		t.Fatal("paying the first invoice consumed the second")
	}
	current, err := a.store.(storage.SubscriptionStorage).CustomerSubscription(ctx, 10001, subs[0].ID)
	if err != nil || current.Snapshot == nil || current.Snapshot.InvoiceID != "" {
		t.Fatal("one-use invoice key leaked into renewal state")
	}
}

func TestSubscriptionAutopayNeverFollowsAnotherAccount(t *testing.T) {
	a, _ := newSubscriptionApp(t)
	ctx := context.Background()
	subs, err := a.discoverSubscriptions(ctx, 10001)
	if err != nil {
		t.Fatal(err)
	}
	snapA := &model.PlanSnapshot{SubscriptionID: subs[0].ID, Months: 1}
	if err := a.store.SetAutoPay(ctx, &model.AutoPay{TelegramID: 10001, Method: "yookassa", MethodID: "card-a", Enabled: true, Months: 1, Snapshot: snapA}); err != nil {
		t.Fatal(err)
	}
	ctxA := remnawave.WithSubscriptionTarget(ctx, 10001, subs[0].ID)
	ctxB := remnawave.WithSubscriptionTarget(ctx, 10001, subs[1].ID)
	if a.getAutoPay(ctxA, 10001) == nil || a.getAutoPay(ctxB, 10001) != nil {
		t.Fatal("autopay followed selected account")
	}
	pay := &yookassa.Payment{ID: "payment-b", Metadata: map[string]string{"autopay": "1"}}
	pay.PaymentMethod.Saved, pay.PaymentMethod.ID = true, "card-b"
	a.saveAutoPayFromPayment(ctxB, 10001, 1, pay, &model.PlanSnapshot{SubscriptionID: subs[1].ID, Months: 1})
	got, err := a.store.GetAutoPay(ctx, 10001)
	if err != nil || got.MethodID != "card-a" || got.Snapshot.SubscriptionID != subs[0].ID {
		t.Fatal("second account replaced an enabled autopay")
	}
}

package storage

import (
	"context"
	"errors"
	"testing"

	"remnabot/internal/model"
)

func TestIndependentSubscriptionOwnership(t *testing.T) {
	eachStore(t, func(t *testing.T, store Storage) {
		ctx := context.Background()
		st := store.(SubscriptionStorage)
		a := &model.CustomerSubscription{TelegramID: 10001, PanelRef: "panel-a", Label: "Personal"}
		b := &model.CustomerSubscription{TelegramID: 10001, PanelRef: "panel-b", Label: "Family"}
		for _, s := range []*model.CustomerSubscription{a, b} {
			if err := st.BindSubscription(ctx, s); err != nil {
				t.Fatal(err)
			}
		}
		if a.ID == "" || a.ID == b.ID {
			t.Fatal("subscription identifiers are not independent")
		}
		if err := st.SelectSubscription(ctx, 10001, a.ID); err != nil {
			t.Fatal(err)
		}
		if err := st.SelectSubscription(ctx, 10002, a.ID); !errors.Is(err, ErrSubscriptionOwner) {
			t.Fatalf("foreign selection accepted: %v", err)
		}
		if s, err := st.CustomerSubscription(ctx, 10002, a.ID); err != nil || s != nil {
			t.Fatal("foreign subscription disclosed")
		}
		if err := st.BindSubscription(ctx, &model.CustomerSubscription{TelegramID: 10002, PanelRef: "panel-a", Label: "Stolen"}); !errors.Is(err, ErrSubscriptionBound) {
			t.Fatal("panel account reassigned")
		}
		deal := &model.PlanSnapshot{SubscriptionID: a.ID, Code: "personal", Months: 1}
		if err := st.SetSubscriptionDeal(ctx, 10001, a.ID, "2027-01-01T00:00:00Z", deal); err != nil {
			t.Fatal(err)
		}
		if err := st.SetSubscriptionDeal(ctx, 10001, b.ID, "2027-01-01T00:00:00Z", deal); !errors.Is(err, ErrSubscriptionData) {
			t.Fatal("deal redirected to another subscription")
		}
		if err := st.SelectSubscription(ctx, 10001, b.ID); err != nil {
			t.Fatal(err)
		}
		got, err := st.CustomerSubscription(ctx, 10001, a.ID)
		if err != nil || got.Snapshot == nil || got.Snapshot.SubscriptionID != a.ID || got.ExpireAt == "" {
			t.Fatal("switching selection altered the original deal")
		}
	})
}

func TestSubscriptionsBackupAndIdentityMove(t *testing.T) {
	ctx := context.Background()
	src := openSQLiteTest(t)
	st := src.(SubscriptionStorage)
	if err := src.UpsertUser(ctx, -10001); err != nil {
		t.Fatal(err)
	}
	sub := &model.CustomerSubscription{TelegramID: -10001, PanelRef: "panel-email", Label: "Personal"}
	if err := st.BindSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	if err := st.SelectSubscription(ctx, -10001, sub.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := src.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	dst := openSQLiteTest(t)
	if err := dst.Import(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := dst.MoveAccount(ctx, -10001, 10001); err != nil {
		t.Fatal(err)
	}
	selected, err := dst.(SubscriptionStorage).SelectedSubscription(ctx, 10001)
	if err != nil || selected == nil || selected.ID != sub.ID || selected.TelegramID != 10001 {
		t.Fatalf("binding lost after backup or email/Telegram linking: %+v %v", selected, err)
	}
	if old, err := dst.(SubscriptionStorage).CustomerSubscriptions(ctx, -10001); err != nil || len(old) != 0 {
		t.Fatal("old identity retained ownership")
	}
}

func TestSubscriptionStaleRefreshAndOccupiedIdentity(t *testing.T) {
	eachStore(t, func(t *testing.T, store Storage) {
		ctx := context.Background()
		st := store.(SubscriptionStorage)
		for _, owner := range []int64{-10001, 10001} {
			if err := store.UpsertUser(ctx, owner); err != nil {
				t.Fatal(err)
			}
		}
		s := &model.CustomerSubscription{TelegramID: -10001, PanelRef: "email-owned", Label: "Personal"}
		if err := st.BindSubscription(ctx, s); err != nil {
			t.Fatal(err)
		}
		before, err := st.CustomerSubscription(ctx, -10001, s.ID)
		if err != nil {
			t.Fatal(err)
		}
		paid := &model.PlanSnapshot{SubscriptionID: s.ID, Months: 3, DeviceLimit: 4}
		if err := st.SetSubscriptionDeal(ctx, -10001, s.ID, "2027-04-01T00:00:00Z", paid); err != nil {
			t.Fatal(err)
		}
		if err := st.RefreshSubscriptionExpiry(ctx, before, "2027-01-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
		got, _ := st.CustomerSubscription(ctx, -10001, s.ID)
		if got.ExpireAt != "2027-04-01T00:00:00Z" || got.Snapshot.Months != 3 {
			t.Fatal("stale panel read overwrote paid terms")
		}
		if err := st.BindSubscription(ctx, &model.CustomerSubscription{TelegramID: 10001, PanelRef: "tg-owned", Label: "Family"}); err != nil {
			t.Fatal(err)
		}
		if err := store.MoveAccount(ctx, -10001, 10001); !errors.Is(err, ErrSubscriptionOwner) {
			t.Fatal("occupied destination was merged", err)
		}
		if got, _ := st.CustomerSubscription(ctx, -10001, s.ID); got == nil {
			t.Fatal("failed identity move changed ownership")
		}
	})
}

package storage

import (
	"context"
	"errors"
	"testing"

	"remnabot/internal/model"
)

func TestSubscriptionPaymentClaimAndAtomicCommit(t *testing.T) {
	eachStore(t, func(t *testing.T, store Storage) {
		ctx := context.Background()
		bindings := store.(SubscriptionStorage)
		ops := store.(SubscriptionOperationStorage)
		a := &model.CustomerSubscription{TelegramID: 10001, PanelRef: "panel-a", Label: "Personal"}
		b := &model.CustomerSubscription{TelegramID: 10001, PanelRef: "panel-b", Label: "Family"}
		for _, sub := range []*model.CustomerSubscription{a, b} {
			if err := bindings.BindSubscription(ctx, sub); err != nil {
				t.Fatal(err)
			}
		}
		snap := &model.PlanSnapshot{SubscriptionID: a.ID, Months: 1, DeviceLimit: 4}
		op := &model.SubscriptionOperation{Method: "test", ID: "charge-a", TelegramID: 10001, SubscriptionID: a.ID,
			Amount: "300", Snapshot: snap, PaymentRef: "charge-a"}
		if err := ops.ClaimSubscriptionOperation(ctx, op); err != nil {
			t.Fatal(err)
		}
		if err := ops.ClaimSubscriptionOperation(ctx, op); !errors.Is(err, ErrSubscriptionHeld) {
			t.Fatal("unfinished operation was automatically retried")
		}
		if err := bindings.SelectSubscription(ctx, 10001, b.ID); err != nil {
			t.Fatal(err)
		}
		payment := &model.Payment{TelegramID: 10001, Method: "test", ExtID: "charge-a", Amount: "300", Months: 1,
			Status: model.PaymentPaid, Snapshot: snap}
		for i := 0; i < 2; i++ {
			if err := ops.CommitSubscriptionOperation(ctx, op.ID, payment, "2027-01-01T00:00:00Z"); err != nil {
				t.Fatal(err)
			}
		}
		if err := ops.ClaimSubscriptionOperation(ctx, op); !errors.Is(err, ErrDuplicateExtID) {
			t.Fatal("completed operation was claimed again")
		}
		gotA, _ := bindings.CustomerSubscription(ctx, 10001, a.ID)
		gotB, _ := bindings.CustomerSubscription(ctx, 10001, b.ID)
		if gotA.ExpireAt == "" || gotA.Snapshot == nil || gotB.ExpireAt != "" || gotB.Snapshot != nil {
			t.Fatal("payment updated the selected account instead of its fixed target")
		}
	})
}

func TestSubscriptionPaymentRollbackAndBackupHold(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteTest(t)
	bindings := store.(SubscriptionStorage)
	ops := store.(SubscriptionOperationStorage)
	sub := &model.CustomerSubscription{TelegramID: 10001, PanelRef: "panel-a", Label: "Personal"}
	if err := bindings.BindSubscription(ctx, sub); err != nil {
		t.Fatal(err)
	}
	snap := &model.PlanSnapshot{SubscriptionID: sub.ID, Months: 1}
	op := &model.SubscriptionOperation{Method: "test", ID: "charge-a", TelegramID: 10001, SubscriptionID: sub.ID,
		Amount: "300", Snapshot: snap, PaymentRef: "charge-a"}
	if err := ops.ClaimSubscriptionOperation(ctx, op); err != nil {
		t.Fatal(err)
	}
	backup, err := store.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	restored := openSQLiteTest(t)
	if err := restored.Import(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if err := restored.(SubscriptionOperationStorage).ClaimSubscriptionOperation(ctx, op); !errors.Is(err, ErrSubscriptionHeld) {
		t.Fatal("backup restoration replayed an uncertain mutation")
	}
	if err := store.DeleteUser(ctx, 10001); err != nil {
		t.Fatal(err)
	}
	payment := &model.Payment{TelegramID: 10001, Method: "test", ExtID: op.PaymentRef, Amount: "300", Status: model.PaymentPaid, Snapshot: snap}
	if err := ops.CommitSubscriptionOperation(ctx, op.ID, payment, "2027-01-01T00:00:00Z"); !errors.Is(err, ErrSubscriptionOwner) {
		t.Fatalf("commit without its binding: %v", err)
	}
	if paid, _ := store.PaymentByExtID(ctx, op.PaymentRef); paid {
		t.Fatal("partial payment was committed without subscription state")
	}
	got, _ := ops.SubscriptionOperation(ctx, op.Method, op.ID)
	if got.Phase != "executing" {
		t.Fatal("failed transaction cleared the durable hold")
	}
}

func TestSubscriptionCancellationRefundsInternalBalanceOnce(t *testing.T) {
	eachStore(t, func(t *testing.T, st Storage) {
		ctx := context.Background()
		if err := st.UpsertUser(ctx, 10001); err != nil {
			t.Fatal(err)
		}
		sub := &model.CustomerSubscription{TelegramID: 10001, PanelRef: "refund-a", Label: "Personal"}
		if err := st.(SubscriptionStorage).BindSubscription(ctx, sub); err != nil {
			t.Fatal(err)
		}
		ops := st.(SubscriptionOperationStorage)
		snap := &model.PlanSnapshot{SubscriptionID: sub.ID, Months: 1, BalanceKopecks: 15000}
		op := &model.SubscriptionOperation{Method: "balance", ID: "refund-a", TelegramID: 10001, SubscriptionID: sub.ID, Amount: "150", Snapshot: snap, PaymentRef: "refund-a"}
		if err := ops.ClaimSubscriptionOperation(ctx, op); err != nil {
			t.Fatal(err)
		}
		if err := ops.SetSubscriptionExpectedExpiry(ctx, op.Method, op.ID, "2027-03-01T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
		if err := ops.SetSubscriptionExpectedExpiry(ctx, op.Method, op.ID, "2027-04-01T00:00:00Z"); !errors.Is(err, ErrSubscriptionHeld) {
			t.Fatal("frozen outbound expiry changed")
		}
		for i := 0; i < 2; i++ {
			if err := ops.CancelSubscriptionOperation(ctx, op.Method, op.ID); err != nil {
				t.Fatal(err)
			}
		}
		u, err := st.GetUser(ctx, 10001)
		if err != nil || u.Balance != 15000 {
			t.Fatalf("refund repeated or lost: %+v %v", u, err)
		}
		if err := ops.ClaimSubscriptionOperation(ctx, op); !errors.Is(err, ErrSubscriptionHeld) {
			t.Fatal("cancelled payment was replayed")
		}
		op.ID, op.PaymentRef = "new-sale", "new-sale"
		if err := ops.ClaimSubscriptionOperation(ctx, op); err != nil {
			t.Fatal("cancel did not release subscription", err)
		}
		if snap.ForRenewal().BalanceKopecks != 0 {
			t.Fatal("refund reserve leaked into renewal")
		}
	})
}

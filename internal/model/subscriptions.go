package model

// CustomerSubscription is an independently owned panel account. A customer may
// have many of these; payment snapshots identify exactly one immutable ID.
type CustomerSubscription struct {
	ID         string
	TelegramID int64
	PanelRef   string
	Label      string
	Kind       string
	ExpireAt   string
	Snapshot   *PlanSnapshot
}

type SubscriptionSelection struct {
	TelegramID     int64
	SubscriptionID string
}

type SubscriptionCallback struct {
	Token          string
	TelegramID     int64
	SubscriptionID string
	Data           string
	ExpiresAt      string
}

// SubscriptionOperation is a durable claim around a panel mutation. A process
// restart must not repeat an operation whose external result is uncertain.
type SubscriptionOperation struct {
	Method         string
	ID             string
	TelegramID     int64
	SubscriptionID string
	Amount         string
	Snapshot       *PlanSnapshot
	Phase          string
	PaymentRef     string
	CreatedAt      string
	ExpectedExpiry string
}

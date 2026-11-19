package remnawave

import (
	"context"
	"errors"
	"net/url"
)

var ErrBoundAccountUnavailable = errors.New("bound panel account is unavailable")
var ErrNewAccountExists = errors.New("new-account invoice cannot modify an existing account")

type newAccountOnlyKey struct{}

type expiryObserverKey struct{}

func WithExpiryObserver(ctx context.Context, observe func(string) error) context.Context {
	return context.WithValue(ctx, expiryObserverKey{}, observe)
}

func WithNewAccountOnly(ctx context.Context, owner int64) context.Context {
	return context.WithValue(ctx, newAccountOnlyKey{}, owner)
}

type subscriptionTargetKey struct{}
type subscriptionTarget struct {
	owner int64
	id    string
}

// WithSubscriptionTarget takes an opaque local ID, never an unchecked panel
// reference. A resolver must verify ownership before any panel request.
func WithSubscriptionTarget(ctx context.Context, owner int64, id string) context.Context {
	ctx = context.WithValue(ctx, subscriptionTargetKey{}, subscriptionTarget{owner, id})
	return context.WithValue(ctx, lookupMemoKey{}, &lookupMemo{seen: map[int64]*panelUser{}})
}

func subscriptionTargetFor(ctx context.Context, owner int64) string {
	target, _ := ctx.Value(subscriptionTargetKey{}).(subscriptionTarget)
	if target.owner == owner {
		return target.id
	}
	return ""
}

func SubscriptionTargetID(ctx context.Context, owner int64) string {
	return subscriptionTargetFor(ctx, owner)
}

// SetSubscriptionResolver is configured once, before the client is shared.
// Empty id requests the current selection; nonempty id is fixed by an invoice.
func (c *Client) SetSubscriptionResolver(resolve func(context.Context, int64, string) (string, error)) {
	c.subscriptionResolver = resolve
}

func (c *Client) findBoundAccount(ctx context.Context, owner int64, ref string) (*panelUser, error) {
	u, err := c.fetchOneRaw(ctx, "/api/users/"+url.PathEscape(ref))
	if err != nil {
		return nil, err
	}
	if u == nil || u.ref().Key() != ref {
		return nil, ErrBoundAccountUnavailable
	}
	if u.TelegramID != 0 && u.TelegramID != owner {
		return nil, ErrAccountOwnerMismatch
	}
	u.boundOwner = owner
	return u, nil
}

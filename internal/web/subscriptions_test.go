package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type testSubscriptionScopeKey struct{}
type subscriptionMini struct {
	*fakeMini
	selectedOwner int64
	selectedID    string
	readTarget    string
}

func (m *subscriptionMini) MiniSubscriptions(context.Context, int64) MiniSubscriptionsDTO {
	return MiniSubscriptionsDTO{Enabled: true, Items: []MiniSubscriptionItemDTO{{ID: "own-a", Label: "Personal"}, {ID: "own-b", Label: "Family"}}}
}
func (m *subscriptionMini) MiniSelectSubscription(_ context.Context, owner int64, id string) MiniActionDTO {
	m.selectedOwner, m.selectedID = owner, id
	return MiniActionDTO{OK: true}
}
func (m *subscriptionMini) MiniSubscriptionScope(ctx context.Context, owner int64, id string) (context.Context, error) {
	if owner != 10001 || (id != "own-a" && id != "own-b") {
		return ctx, errors.New("unavailable")
	}
	return context.WithValue(ctx, testSubscriptionScopeKey{}, id), nil
}
func (m *subscriptionMini) MiniSubscription(ctx context.Context, _ int64) MiniSubDTO {
	m.readTarget, _ = ctx.Value(testSubscriptionScopeKey{}).(string)
	return MiniSubDTO{Active: true}
}

func TestSubscriptionAPIUsesAuthenticatedOwnerAndTarget(t *testing.T) {
	for _, tc := range []struct {
		name, token, target string
		code                int
	}{
		{"unauthenticated", "", "own-a", http.StatusUnauthorized},
		{"owned", webToken(&fakeMini{}, 10001, 0), "own-a", http.StatusOK},
		{"foreign", webToken(&fakeMini{}, 10001, 0), "foreign", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &subscriptionMini{fakeMini: &fakeMini{}}
			srv := newServer(nil, nil)
			srv.allowPlainHTTP = true
			srv.SetMiniApp(m)
			r := gateReq(http.MethodGet, "/api/miniapp/subscription?telegram_id=99999", "", tc.token, "203.0.113.70")
			r.Header.Set("X-Subscription-ID", tc.target)
			w := httptest.NewRecorder()
			srv.wrap(srv.mux()).ServeHTTP(w, r)
			if w.Code != tc.code {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.code == http.StatusOK && m.readTarget != "own-a" {
				t.Fatal("request lost its explicit subscription scope")
			}
			if tc.code != http.StatusOK && m.readTarget != "" {
				t.Fatal("unauthorised request reached subscription data")
			}
		})
	}
}

func TestSubscriptionSelectionIgnoresOwnerFromBody(t *testing.T) {
	m := &subscriptionMini{fakeMini: &fakeMini{}}
	srv := newServer(nil, nil)
	srv.allowPlainHTTP = true
	srv.SetMiniApp(m)
	r := gateReq(http.MethodPost, "/api/miniapp/subscriptions/select", `{"id":"own-b","telegram_id":99999}`, webToken(m.fakeMini, 10001, 0), "203.0.113.70")
	w := httptest.NewRecorder()
	srv.wrap(srv.mux()).ServeHTTP(w, r)
	if w.Code != http.StatusOK || m.selectedOwner != 10001 || m.selectedID != "own-b" {
		t.Fatalf("body substituted the authenticated customer: status=%d owner=%d", w.Code, m.selectedOwner)
	}
}

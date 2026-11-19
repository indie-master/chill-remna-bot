package remnawave

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFrozenSubscriptionOverridesCurrentSelection(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		_, _ = fmt.Fprintf(w, `{"response":{"uuid":%q,"telegramId":10001}}`, r.URL.Path[len("/api/users/"):])
	}))
	defer srv.Close()
	c := testClient(srv.URL)
	c.SetSubscriptionResolver(func(ctx context.Context, owner int64, id string) (string, error) {
		if owner != 10001 {
			return "", ErrAccountOwnerMismatch
		}
		switch id {
		case "invoice-a":
			return "panel-a", nil
		case "":
			return "panel-b", nil
		default:
			return "", ErrAccountOwnerMismatch
		}
	})
	ctx := WithLookupMemo(context.Background())
	if _, err := c.FindByTelegramID(ctx, 10001); err != nil {
		t.Fatal(err)
	}
	ctx = WithSubscriptionTarget(ctx, 10001, "invoice-a")
	account, err := c.FindByTelegramID(ctx, 10001)
	if err != nil || account.Ref.Key() != "panel-a" || len(paths) != 2 || paths[1] != "/api/users/panel-a" {
		t.Fatalf("invoice followed current selection: %+v %v %v", account, err, paths)
	}
	if _, err := c.FindByTelegramID(WithSubscriptionTarget(ctx, 10001, "foreign"), 10001); !errors.Is(err, ErrAccountOwnerMismatch) || len(paths) != 2 {
		t.Fatal("unverified invoice target reached the panel")
	}
}

func TestBoundAccountMissingNeverCreatesReplacement(t *testing.T) {
	writes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes++
		}
		_, _ = w.Write([]byte(`{"response":null}`))
	}))
	defer srv.Close()
	c := testClient(srv.URL)
	c.SetSubscriptionResolver(func(context.Context, int64, string) (string, error) { return "panel-a", nil })
	_, _, err := c.ExtendUserDays(context.Background(), 10001, 3, 0, UserLimits{}, false)
	if !errors.Is(err, ErrBoundAccountUnavailable) || writes != 0 {
		t.Fatalf("missing bound account caused a replacement: %v writes=%d", err, writes)
	}
}

func TestCustomerAccountsRejectsForeignAndPartialInventory(t *testing.T) {
	for _, tc := range []struct {
		name, body     string
		modern, wantOK bool
	}{
		{"legacy", `{"response":[{"uuid":"a","telegramId":10001},{"uuid":"b","telegramId":10001}]}`, false, true},
		{"modern", `{"response":{"users":[{"id":12,"telegramId":10001},{"id":13,"telegramId":10001}],"nextCursor":null}}`, true, true},
		{"foreign", `{"response":{"users":[{"id":12,"telegramId":10002}]}}`, true, false},
		{"partial", `{"response":{"users":[{"id":12,"telegramId":10001}],"nextCursor":"more"}}`, true, false},
		{"has-more", `{"response":{"users":[{"id":12,"telegramId":10001}],"hasMore":true}}`, true, false},
		{"missing-page", `{"response":{}}`, true, false},
		{"duplicate", `{"response":[{"uuid":"a","telegramId":10001},{"uuid":"a","telegramId":10001}]}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.modern && (r.URL.Path != "/api/users/stream" || r.URL.Query().Get("telegramId") != "10001") {
					t.Error("inventory request lost verified owner")
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := testClient(srv.URL)
			if tc.modern {
				c.noteGen(genV3)
			}
			got, err := c.CustomerAccounts(context.Background(), 10001)
			if tc.wantOK && (err != nil || len(got) != 2) {
				t.Fatalf("inventory: %+v %v", got, err)
			}
			if !tc.wantOK && (err == nil || len(got) != 0) {
				t.Fatal("unsafe inventory accepted")
			}
		})
	}
}

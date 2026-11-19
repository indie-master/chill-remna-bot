package remnawave

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAmbiguousTelegramLookupNeverWrites(t *testing.T) {
	for _, gen := range []panelGen{genLegacy, genV3} {
		t.Run(fmt.Sprint(gen), func(t *testing.T) {
			writes := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes++
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				if gen == genV3 {
					if r.URL.Query().Get("size") != "2" {
						t.Error("lookup cannot detect a second account")
					}
					_, _ = w.Write([]byte(`{"response":{"users":[{"id":11,"telegramId":12345},{"id":12,"telegramId":12345}]}}`))
				} else {
					_, _ = w.Write([]byte(`{"response":[{"uuid":"one","telegramId":12345},{"uuid":"two","telegramId":12345}]}`))
				}
			}))
			defer srv.Close()
			c := testClient(srv.URL)
			c.noteGen(gen)
			_, _, err := c.ExtendUserDays(context.Background(), 12345, 3, 0, UserLimits{}, false)
			if !errors.Is(err, ErrAmbiguousAccount) || writes != 0 {
				t.Fatalf("err=%v writes=%d; ambiguous accounts must remain unchanged", err, writes)
			}
		})
	}
}

func TestLegacyTelegramOwnerMismatch(t *testing.T) {
	for _, body := range []string{
		`{"response":[{"uuid":"other","telegramId":99999}]}`,
		`{"response":{"uuid":"other","telegramId":99999}}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		c := testClient(srv.URL)
		_, err := c.FindByTelegramID(context.Background(), 12345)
		srv.Close()
		if !errors.Is(err, ErrAccountOwnerMismatch) {
			t.Fatalf("wrong account accepted: %v", err)
		}
	}
}

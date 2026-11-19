package remnawave

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
)

// CustomerAccounts reads all accounts in the bounded result. It never returns a
// partial page as a complete inventory and never trusts an ignored owner filter.
func (c *Client) CustomerAccounts(ctx context.Context, owner int64) ([]PanelUser, error) {
	if owner <= 0 {
		return nil, ErrAccountOwnerMismatch
	}
	const limit = 100
	stream := c.generation() == genV3
	path := "/api/users/by-telegram-id/" + strconv.FormatInt(owner, 10)
	if stream {
		path = "/api/users/stream?size=101&telegramId=" + strconv.FormatInt(owner, 10)
	}
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && !stream {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if err != nil {
			return nil, err
		}
		if !routeGone(body) {
			return []PanelUser{}, nil
		}
		// Retry only the read, in the newer dialect, once.
		c.noteGen(genV3)
		return c.CustomerAccounts(ctx, owner)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, classifyHTTP(resp)
	}
	var envelope struct {
		Response json.RawMessage `json:"response"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&envelope); err != nil {
		return nil, err
	}
	var users []panelUser
	if stream {
		var page struct {
			Users      []panelUser     `json:"users"`
			NextCursor json.RawMessage `json:"nextCursor"`
			HasMore    bool            `json:"hasMore"`
		}
		if err := json.Unmarshal(envelope.Response, &page); err != nil {
			return nil, err
		}
		if page.Users == nil || page.HasMore || len(page.NextCursor) > 0 && string(page.NextCursor) != "null" && string(page.NextCursor) != "0" {
			return nil, errors.New("customer account inventory exceeds the supported page")
		}
		users = page.Users
	} else if err := json.Unmarshal(envelope.Response, &users); err != nil {
		var one panelUser
		if err := json.Unmarshal(envelope.Response, &one); err != nil || one.ref().Empty() {
			return nil, errors.New("invalid customer account inventory")
		}
		users = []panelUser{one}
	}
	if len(users) > limit {
		return nil, errors.New("customer account inventory exceeds the supported limit")
	}
	out := make([]PanelUser, 0, len(users))
	seen := map[string]bool{}
	for i := range users {
		u := &users[i]
		if u.TelegramID != owner || u.ref().Empty() || seen[u.ref().Key()] {
			return nil, ErrAccountOwnerMismatch
		}
		seen[u.ref().Key()] = true
		c.noteUser(u)
		out = append(out, *toPanelUser(u))
	}
	return out, nil
}

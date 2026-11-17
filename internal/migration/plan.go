// Package migration builds an offline review document. It has no panel client,
// database writer or payment side effects. The document is not an import job.
package migration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Inventory must be assembled from every page of one panel export. Complete
// is an operator assertion; Total must equal the panel's authoritative count.
type Inventory struct {
	Version  int       `json:"version"`
	Complete bool      `json:"complete"`
	Total    *int      `json:"total"`
	Users    []Account `json:"users"`
}

// Only allowlisted fields can enter a plan. Subscription URLs, credentials,
// descriptions, device identifiers and panel-provided emails are not copied.
type Account struct {
	UUID                 string      `json:"uuid,omitempty"`
	ID                   json.Number `json:"id,omitempty"`
	TelegramID           json.Number `json:"telegramId,omitempty"`
	Username             string      `json:"username"`
	Status               string      `json:"status"`
	ExpireAt             string      `json:"expireAt"`
	TrafficLimitBytes    int64       `json:"trafficLimitBytes"`
	TrafficLimitStrategy string      `json:"trafficLimitStrategy"`
	HwidDeviceLimit      *int        `json:"hwidDeviceLimit"`
	ActiveInternalSquads []Squad     `json:"activeInternalSquads"`
	ExternalSquadUUID    string      `json:"externalSquadUuid,omitempty"`
}

type Squad struct {
	UUID string      `json:"uuid,omitempty"`
	ID   json.Number `json:"id,omitempty"`
}

type Claim struct {
	PanelRef string `json:"panelRef"`
	Customer string `json:"customer"` // tg:12345 or email:verified@example.invalid
	Verified bool   `json:"verified"` // operator-reviewed evidence, never name similarity
}

type Subscription struct {
	PanelRef             string   `json:"panelRef"`
	Username             string   `json:"username"`
	Status               string   `json:"status"`
	ExpireAt             string   `json:"expireAt"`
	TrafficLimitBytes    int64    `json:"trafficLimitBytes"`
	TrafficLimitStrategy string   `json:"trafficLimitStrategy"`
	HwidDeviceLimit      *int     `json:"hwidDeviceLimit"`
	InternalSquads       []string `json:"internalSquads"`
	ExternalSquadUUID    string   `json:"externalSquadUuid,omitempty"`
}

type Customer struct {
	Key           string         `json:"key"`
	TrialEligible bool           `json:"trialEligible"`
	Subscriptions []Subscription `json:"subscriptions"`
}

type Issue struct {
	Code     string `json:"code"`
	PanelRef string `json:"panelRef"`
}

type Plan struct {
	Version      int            `json:"version"`
	Ready        bool           `json:"ready"`
	SourceSHA256 string         `json:"sourceSha256"`
	Customers    []Customer     `json:"customers"`
	Unassigned   []Subscription `json:"unassigned"`
	Issues       []Issue        `json:"issues"`
}

func reference(uuid string, id json.Number) (string, error) {
	if uuid != "" {
		// Older panels use UUIDs; keep this opaque rather than converting it.
		if strings.TrimSpace(uuid) != uuid || len(uuid) > 128 || strings.ContainsAny(uuid, "/\\\x00\r\n") {
			return "", errors.New("invalid panel reference")
		}
		return uuid, nil
	}
	n, err := strconv.ParseInt(string(id), 10, 64)
	if err != nil || n <= 0 {
		return "", errors.New("missing stable panel reference")
	}
	return strconv.FormatInt(n, 10), nil
}

func customerKey(raw string) (string, error) {
	kind, value, ok := strings.Cut(raw, ":")
	if !ok {
		return "", errors.New("customer key must use tg: or email:")
	}
	switch kind {
	case "tg":
		n, err := strconv.ParseInt(value, 10, 64)
		if err == nil && n > 0 {
			return "tg:" + strconv.FormatInt(n, 10), nil
		}
	case "email":
		value = strings.ToLower(strings.TrimSpace(value))
		addr, err := mail.ParseAddress(value)
		if err == nil && addr.Address == value && addr.Name == "" {
			return "email:" + value, nil
		}
	}
	return "", errors.New("invalid customer key")
}

// Build groups every account without choosing a primary subscription. Conflicts
// block the affected account; unresolved ownership is never guessed. Existing
// customers are always marked ineligible for a new-customer trial.
func Build(raw []byte, claims []Claim) (*Plan, error) {
	var inventory Inventory
	if err := json.Unmarshal(raw, &inventory); err != nil {
		return nil, errors.New("invalid inventory JSON")
	}
	if inventory.Version != 1 || !inventory.Complete || inventory.Total == nil || *inventory.Total != len(inventory.Users) {
		return nil, errors.New("inventory must contain every page and the matching authoritative total (version 1)")
	}
	p := &Plan{Version: 1, Customers: []Customer{}, Unassigned: []Subscription{}, Issues: []Issue{}}
	digest := sha256.Sum256(raw)
	p.SourceSHA256 = hex.EncodeToString(digest[:])
	known := map[string]bool{}
	for _, a := range inventory.Users {
		ref, err := reference(a.UUID, a.ID)
		if err != nil {
			return nil, err
		}
		if known[ref] {
			return nil, errors.New("duplicate panel reference in inventory")
		}
		known[ref] = true
	}
	owners := map[string]string{}
	blocked := map[string]bool{}
	for _, claim := range claims {
		if !known[claim.PanelRef] {
			return nil, errors.New("claim references an account outside the inventory")
		}
		if !claim.Verified {
			blocked[claim.PanelRef] = true
			p.Issues = append(p.Issues, Issue{"UNVERIFIED_CLAIM", claim.PanelRef})
			continue
		}
		key, err := customerKey(claim.Customer)
		if err != nil {
			return nil, err
		}
		if prev := owners[claim.PanelRef]; prev != "" && prev != key {
			blocked[claim.PanelRef] = true
			p.Issues = append(p.Issues, Issue{"CONFLICTING_CLAIMS", claim.PanelRef})
		}
		owners[claim.PanelRef] = key
	}
	groups := map[string][]Subscription{}
	for _, a := range inventory.Users {
		ref, _ := reference(a.UUID, a.ID)
		if _, err := time.Parse(time.RFC3339Nano, a.ExpireAt); err != nil {
			return nil, errors.New("account expiry must be an RFC3339 timestamp")
		}
		if a.TrafficLimitBytes < 0 || (a.HwidDeviceLimit != nil && *a.HwidDeviceLimit < 0) {
			return nil, errors.New("invalid account limits")
		}
		sub := Subscription{PanelRef: ref, Username: a.Username, Status: a.Status, ExpireAt: a.ExpireAt,
			TrafficLimitBytes: a.TrafficLimitBytes, TrafficLimitStrategy: a.TrafficLimitStrategy,
			HwidDeviceLimit: a.HwidDeviceLimit, InternalSquads: []string{}, ExternalSquadUUID: a.ExternalSquadUUID}
		for _, squad := range a.ActiveInternalSquads {
			sref, err := reference(squad.UUID, squad.ID)
			if err != nil {
				return nil, fmt.Errorf("invalid internal squad reference")
			}
			sub.InternalSquads = append(sub.InternalSquads, sref)
		}
		key := owners[ref]
		if a.TelegramID != "" && a.TelegramID != "0" {
			tg, err := customerKey("tg:" + string(a.TelegramID))
			if err != nil {
				return nil, errors.New("invalid panel Telegram ID")
			}
			if key != "" && key != tg {
				blocked[ref] = true
				p.Issues = append(p.Issues, Issue{"PANEL_OWNER_CONFLICT", ref})
			}
			key = tg
		}
		if key == "" || blocked[ref] {
			p.Unassigned = append(p.Unassigned, sub)
		} else {
			groups[key] = append(groups[key], sub)
		}
	}
	for key, subs := range groups {
		sort.Slice(subs, func(i, j int) bool { return subs[i].PanelRef < subs[j].PanelRef })
		p.Customers = append(p.Customers, Customer{Key: key, TrialEligible: false, Subscriptions: subs})
	}
	sort.Slice(p.Customers, func(i, j int) bool { return p.Customers[i].Key < p.Customers[j].Key })
	sort.Slice(p.Unassigned, func(i, j int) bool { return p.Unassigned[i].PanelRef < p.Unassigned[j].PanelRef })
	sort.Slice(p.Issues, func(i, j int) bool {
		if p.Issues[i].PanelRef != p.Issues[j].PanelRef {
			return p.Issues[i].PanelRef < p.Issues[j].PanelRef
		}
		return p.Issues[i].Code < p.Issues[j].Code
	})
	p.Ready = len(p.Unassigned) == 0 && len(p.Issues) == 0
	return p, nil
}

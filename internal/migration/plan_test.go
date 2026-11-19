package migration

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func inventoryJSON(t *testing.T, users []Account) []byte {
	t.Helper()
	total := len(users)
	b, err := json.Marshal(Inventory{Version: 1, Complete: true, Total: &total, Users: users})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func account(ref, tg, name string) Account {
	devices := 4
	return Account{UUID: ref, TelegramID: json.Number(tg), Username: name, Status: "ACTIVE",
		ExpireAt: "2027-03-05T09:00:00.000Z", TrafficLimitBytes: 17179869184,
		TrafficLimitStrategy: "DAY", HwidDeviceLimit: &devices,
		ActiveInternalSquads: []Squad{{UUID: "squad-example"}}, ExternalSquadUUID: "external-example"}
}

func TestIndependentSubscriptionsPreserved(t *testing.T) {
	a, b := account("sub-a", "10001", "personal"), account("sub-b", "10001", "family")
	b.ExpireAt = "2027-06-01T12:30:00Z"
	b.TrafficLimitBytes = 0
	plan, err := Build(inventoryJSON(t, []Account{b, a}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Ready || len(plan.Customers) != 1 || len(plan.Customers[0].Subscriptions) != 2 || plan.Customers[0].TrialEligible {
		t.Fatalf("independent subscriptions lost or trial reissued: %+v", plan)
	}
	subs := plan.Customers[0].Subscriptions
	if subs[0].PanelRef != "sub-a" || subs[1].ExpireAt != b.ExpireAt || subs[1].TrafficLimitBytes != 0 || *subs[0].HwidDeviceLimit != 4 || !reflect.DeepEqual(subs[0].InternalSquads, []string{"squad-example"}) {
		t.Fatal("existing subscription terms were altered")
	}
}

func TestNoNameOrEmailGuessing(t *testing.T) {
	users := []Account{account("sub-a", "", "same-name"), account("sub-b", "10001", "same-name")}
	raw := inventoryJSON(t, users)
	// Unknown fields, including subscription secrets, must never enter output.
	raw = []byte(strings.Replace(string(raw), `"username":"same-name"`, `"email":"unverified@example.invalid","subscriptionUrl":"SECRET-LINK","password":"SECRET-PASSWORD","username":"same-name"`, 1))
	plan, err := Build(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Ready || len(plan.Unassigned) != 1 || len(plan.Customers) != 1 {
		t.Fatal("ownership inferred from profile data")
	}
	out, _ := json.Marshal(plan)
	if strings.Contains(string(out), "SECRET-") || strings.Contains(string(out), "unverified@example") {
		t.Fatal("non-allowlisted profile fields leaked")
	}
}

func TestOwnershipClaims(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tg     string
		claims []Claim
		ready  bool
		code   string
	}{
		{"verified-email", "", []Claim{{"sub-a", "email:Client@example.invalid", true}}, true, ""},
		{"verified-tg", "", []Claim{{"sub-a", "tg:10001", true}}, true, ""},
		{"unverified", "10001", []Claim{{"sub-a", "tg:10001", false}}, false, "UNVERIFIED_CLAIM"},
		{"panel-conflict", "10001", []Claim{{"sub-a", "tg:10002", true}}, false, "PANEL_OWNER_CONFLICT"},
		{"claim-conflict", "", []Claim{{"sub-a", "tg:10001", true}, {"sub-a", "tg:10002", true}}, false, "CONFLICTING_CLAIMS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := Build(inventoryJSON(t, []Account{account("sub-a", tc.tg, "client")}), tc.claims)
			if err != nil || plan.Ready != tc.ready {
				t.Fatalf("ready mismatch: plan=%+v err=%v", plan, err)
			}
			if tc.code != "" && (len(plan.Issues) == 0 || plan.Issues[0].Code != tc.code || len(plan.Customers) != 0) {
				t.Fatalf("conflicting owner was accepted: %+v", plan)
			}
		})
	}
}

func TestInvalidInventoryRejected(t *testing.T) {
	for _, raw := range []string{
		`[]`, `null`, `{}`, `{"version":1,"total":0,"users":[]}`,
		`{"version":1,"complete":true,"users":[]}`,
		`{"version":1,"complete":true,"total":2,"users":[]}`,
		`{"version":1,"complete":true,"total":1,"users":[{"username":"client"}]}`,
	} {
		if _, err := Build([]byte(raw), nil); err == nil {
			t.Fatalf("incomplete or invalid export accepted: %s", raw)
		}
	}
	a := account("sub-a", "10001", "client")
	if _, err := Build(inventoryJSON(t, []Account{a, a}), nil); err == nil {
		t.Fatal("duplicate account accepted")
	}
	if _, err := Build(inventoryJSON(t, []Account{a}), []Claim{{"unknown", "tg:10001", true}}); err == nil {
		t.Fatal("claim outside inventory accepted")
	}
}

func TestNumericPanelIdentifiers(t *testing.T) {
	a := account("", "10001", "client")
	a.ID = "9876543210123"
	a.ActiveInternalSquads = []Squad{{ID: "9876543210124"}}
	plan, err := Build(inventoryJSON(t, []Account{a}), nil)
	if err != nil || plan.Customers[0].Subscriptions[0].PanelRef != "9876543210123" || plan.Customers[0].Subscriptions[0].InternalSquads[0] != "9876543210124" {
		t.Fatalf("numeric identifiers changed: %+v %v", plan, err)
	}
}

func TestDeterministicGrouping(t *testing.T) {
	raw := inventoryJSON(t, []Account{account("sub-c", "10002", "c"), account("sub-b", "10001", "b"), account("sub-a", "10001", "a")})
	one, _ := Build(raw, nil)
	for i := 0; i < 20; i++ {
		next, _ := Build(raw, nil)
		if !reflect.DeepEqual(one, next) {
			t.Fatal("migration preview order is unstable")
		}
	}
}

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"remnabot/internal/config"
	"remnabot/internal/web"
)

func TestConnectCatalogSuppliesHappAndIncyForSelectedSubscription(t *testing.T) {
	const sub = "https://subscriptions.example.invalid/selected?service=a&profile=b#name"
	a := &App{}
	platforms := a.addConnectCatalog(nil, sub, "", "ru")
	if len(platforms) != 2 {
		t.Fatalf("expected Android and iOS, got %+v", platforms)
	}
	for _, p := range platforms {
		if len(p.Apps) != 2 {
			t.Fatalf("%s expected two apps, got %+v", p.Key, p.Apps)
		}
		for _, app := range p.Apps {
			if want := strings.ToLower(app.Name) + "://add/" + sub; app.Deeplink != want {
				t.Errorf("%s: got %q, want %q", app.Name, app.Deeplink, want)
			}
			if len(app.More) < 2 || len(app.Installs) == 0 {
				t.Errorf("%s lacks install or manual instructions", app.Name)
			}
			if p.Key == "android" {
				found := false
				for _, install := range app.Installs {
					if strings.HasSuffix(install.URL, ".apk") {
						found = true
					}
				}
				if !found {
					t.Errorf("%s lacks Android APK", app.Name)
				}
			}
		}
	}
	if apps := a.addConnectCatalog(nil, "", "", "ru"); len(apps) != 0 {
		t.Fatal("catalog must not invent a missing subscription")
	}
}

func TestConnectCatalogLocalOverridesKeepOtherPanelApps(t *testing.T) {
	dir := t.TempDir()
	a := &App{cfg: &config.Config{DataDir: dir}}
	panel := []web.MiniConnectPlatformDTO{{Key: "iOS", Label: "Apple", Apps: []web.MiniConnectAppDTO{{Name: "Happ - Proxy Utility", Deeplink: "happ://legacy"}, {Name: "Panel App", Deeplink: "another://add"}}}}
	path := filepath.Join(dir, connectCatalogName)
	local := `{"platforms":{"ios":{"apps":[{"name":"Happ","blocks":[{"buttons":[{"type":"external","link":"https://apps.example.invalid/happ"}]},{"buttons":[{"type":"subscriptionLink","link":"happ://add/{{SUBSCRIPTION_LINK}}"}]}]},{"name":"Custom Client","blocks":[{"buttons":[{"type":"external","link":"https://apps.example.invalid/custom"}]}]}]}}}`
	if err := os.WriteFile(path, []byte(local), 0600); err != nil {
		t.Fatal(err)
	}
	const sub = "https://subscriptions.example.invalid/abc"
	got := a.addConnectCatalog(panel, sub, "", "ru")
	if len(got) != 2 || got[1].Key != "ios" || got[1].Label != "Apple" || len(got[1].Apps) != 4 {
		t.Fatalf("catalog merge: %+v", got)
	}
	if app := got[1].Apps[0]; app.Name != "Happ" || app.Installs[0].URL != "https://apps.example.invalid/happ" {
		t.Fatalf("operator override was lost: %+v", app)
	}
	if got[1].Apps[1].Name != "Custom Client" || got[1].Apps[3].Name != "Panel App" {
		t.Fatal("local extension or panel app was lost")
	}
	if err := os.WriteFile(path, []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := a.addConnectCatalog(panel, sub, "", "ru"); len(got[1].Apps) != 3 {
		t.Fatal("malformed local file removed defaults")
	}
	unsafe := strings.Replace(local, "https://apps.example.invalid/happ", "https://apps.example.invalid/?sub={{SUBSCRIPTION_LINK}}", 1)
	if err := os.WriteFile(path, []byte(unsafe), 0600); err != nil {
		t.Fatal(err)
	}
	if readConnectCatalog(dir) != nil {
		t.Fatal("invalid download template exposes the subscription link")
	}
}

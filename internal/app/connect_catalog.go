package app

import (
	_ "embed"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"remnabot/internal/web"
)

//go:embed connect_apps.json
var defaultConnectApps []byte

const connectCatalogName = "connect-apps.json"

func (a *App) addConnectCatalog(platforms []web.MiniConnectPlatformDTO, subscription, username, lang string) []web.MiniConnectPlatformDTO {
	// A standard subscription is an absolute HTTPS (or operator-configured HTTP)
	// URL. Do not create new import actions for any other kind of panel response.
	u, err := url.Parse(subscription)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return platforms
	}
	var defaults appConfigV2
	if json.Unmarshal(defaultConnectApps, &defaults) != nil {
		return platforms
	}
	platforms = mergeConnectPlatforms(platforms, buildV2Platforms(&defaults, subscription, username, lang))
	if a.cfg != nil && a.cfg.DataDir != "" {
		if custom := readConnectCatalog(a.cfg.DataDir); custom != nil {
			platforms = mergeConnectPlatforms(platforms, buildV2Platforms(custom, subscription, username, lang))
		}
	}
	return platforms
}

// The catalog is an operator file in the data volume. It is not an HTTP asset.
func readConnectCatalog(directory string) *appConfigV2 {
	const maxSize = 128 << 10
	path := filepath.Join(directory, connectCatalogName)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSize {
		return nil
	}
	// #nosec G304 -- directory and filename are controlled by the operator.
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil || len(data) > maxSize {
		return nil
	}
	var cfg appConfigV2
	if json.Unmarshal(data, &cfg) != nil || len(cfg.Platforms) == 0 {
		return nil
	}
	for _, platform := range cfg.Platforms {
		for _, app := range platform.Apps {
			for _, block := range app.Blocks {
				for _, button := range block.Buttons {
					if button.Type == "external" && !staticConnectURL(button.Link) {
						return nil
					}
				}
			}
		}
	}
	return &cfg
}

func staticConnectURL(link string) bool {
	if strings.Contains(link, "{{") {
		return false
	}
	u, err := url.Parse(link)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil
}

func connectAppKey(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	switch key {
	case "happ - proxy utility", "happ proxy utility":
		return "happ"
	case "incy vpn", "incy - vpn", "инси":
		return "incy"
	default:
		return key
	}
}

// Prefer the local/default app card on matching names. Keep all other apps
// from the panel and all platform labels. Normalize case for OS detection.
func mergeConnectPlatforms(base, overlay []web.MiniConnectPlatformDTO) []web.MiniConnectPlatformDTO {
	platforms := make(map[string]web.MiniConnectPlatformDTO)
	keys := make(map[string]bool)
	for _, source := range [][]web.MiniConnectPlatformDTO{base, overlay} {
		for _, p := range source {
			key := strings.ToLower(p.Key)
			keys[key] = true
			current := platforms[key]
			if current.Label == "" {
				current.Label = p.Label
			}
			current.Key = key
			seen := make(map[string]bool)
			apps := make([]web.MiniConnectAppDTO, 0, len(p.Apps)+len(current.Apps))
			for _, entries := range [][]web.MiniConnectAppDTO{p.Apps, current.Apps} {
				for _, app := range entries {
					id := connectAppKey(app.Name)
					if id != "" && !seen[id] {
						seen[id] = true
						apps = append(apps, app)
					}
				}
			}
			current.Apps = apps
			platforms[key] = current
		}
	}
	var out []web.MiniConnectPlatformDTO
	for _, key := range orderedPlatformKeys(keys) {
		out = append(out, platforms[key])
	}
	return out
}

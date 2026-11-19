package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubCLIRelease(t *testing.T) {
	const assetPath = "/download/cli-v0.10.0/esp32agent_windows_amd64.zip"
	for _, test := range []struct {
		name, version, path, wantErr string
	}{
		{"published CLI", "0.10.0", assetPath, ""},
		{"version mismatch", "2.0.0", assetPath, "does not match"},
		{"unrelated asset", "0.10.0", "/other/download.zip", "unexpected asset URL"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/releases":
					switch r.URL.Query().Get("page") {
					case "1":
						w.Header().Set("Link", `</releases?page=2>; rel="next"`)
						fmt.Fprint(w, `[{"tag_name":"v99.0.0"},{"tag_name":"cli-v2.0.0","draft":true},{"tag_name":"cli-v3.0.0","prerelease":true},{"tag_name":"cli-v1.0.0-rc.1"},{"tag_name":"cli-v0.9.4"}]`)
					case "2":
						fmt.Fprint(w, `[{"tag_name":"cli-v0.10.0"}]`)
					default:
						http.NotFound(w, r)
					}
				case "/download/cli-v0.10.0/versions.json":
					fmt.Fprintf(w, `{"latest":%q,"versions":[{"version":%q,"assets":{"windows_amd64":{"url":%q,"sha256":%q}}}]}`,
						test.version, test.version, "http://"+r.Host+test.path, strings.Repeat("b", 64))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			idx, err := fetchGitHub(context.Background(), server.URL+"/releases", server.URL+"/download")
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("expected %q, got %v", test.wantErr, err)
				}
				return
			}
			if err != nil || idx.Latest != test.version {
				t.Fatalf("release lookup: index=%v err=%v", idx, err)
			}
		})
	}
}

func TestGitHubRateLimitFallback(t *testing.T) {
	const version = "0.3.1"
	const tag = "cli-v" + version
	for _, tc := range []struct {
		name           string
		status         int
		tag, version   string
		asset, wantErr string
	}{
		{"forbidden", 403, tag, version, "cli", ""},
		{"rate limited", 429, tag, version, "cli", ""},
		{"additional platform", 429, tag, version, "linux-arm64", ""},
		{"server error", 503, tag, version, "cli", "HTTP 503"},
		{"firmware tag", 403, "firmware-v7", version, "cli", "does not resolve"},
		{"missing manifest", 429, tag, version, "missing", "HTTP 404"},
		{"prerelease", 403, "cli-v0.3.1-rc.1", "0.3.1-rc.1", "cli", "no stable CLI version"},
		{"noncanonical version", 403, "cli-v00.3.1", "00.3.1", "cli", "no stable CLI version"},
		{"other host", 429, tag, version, "external", "unexpected asset URL"},
		{"wrong asset version", 403, tag, version, "older", "unexpected asset URL"},
		{"empty assets", 403, tag, version, "empty", "no CLI assets"},
		{"unsafe platform", 403, tag, version, "unsafe-key", "unexpected CLI platform"},
		{"wrong archive format", 403, tag, version, "wrong-ext", "unexpected asset URL"},
		{"bad digest", 403, tag, version, "bad-sha", "invalid SHA-256"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			server := httptest.NewServer(mux)
			defer server.Close()
			downloads := server.URL + "/releases/download"
			mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			})
			tagPath := "/releases/download/" + tc.tag + "/versions.json"
			mux.HandleFunc("/releases/latest/download/versions.json", func(w http.ResponseWriter, r *http.Request) {
				if tc.status == 503 {
					t.Error("unexpected fallback after HTTP 503")
				}
				http.Redirect(w, r, tagPath, http.StatusFound)
			})
			mux.HandleFunc(tagPath, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/storage/manifest", http.StatusFound)
			})
			mux.HandleFunc("/storage/manifest", func(w http.ResponseWriter, r *http.Request) {
				asset := Asset{URL: downloads + "/" + tag + "/esp32agent_windows_amd64.zip", SHA256: strings.Repeat("a", 64)}
				assets := map[string]Asset{}
				switch tc.asset {
				case "missing":
					http.NotFound(w, r)
					return
				case "external":
					asset.URL = "https://example.org/esp32agent_windows_amd64.zip"
				case "older":
					asset.URL = strings.Replace(asset.URL, version, "0.2.0", 1)
				case "bad-sha":
					asset.SHA256 = "x"
				case "wrong-ext":
					asset.URL = strings.TrimSuffix(asset.URL, ".zip") + ".tar.gz"
				case "linux-arm64":
					for _, platform := range []string{"darwin_arm64", "linux_amd64", "linux_arm64"} {
						assets[platform] = Asset{URL: downloads + "/" + tag + "/esp32agent_" + platform + ".tar.gz", SHA256: asset.SHA256}
					}
				case "unsafe-key":
					platform := "../linux_arm64"
					assets[platform] = Asset{URL: downloads + "/" + tag + "/esp32agent_" + platform + ".tar.gz", SHA256: asset.SHA256}
				}
				if tc.asset != "empty" {
					assets["windows_amd64"] = asset
				}
				json.NewEncoder(w).Encode(Index{Latest: tc.version, Versions: []Release{{Version: tc.version, Assets: assets}}})
			})
			idx, err := fetchGitHub(context.Background(), server.URL+"/api", downloads)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
			} else if err != nil || idx.Latest != version {
				t.Fatalf("expected version %s, got %v (%v)", version, idx, err)
			}
		})
	}
}

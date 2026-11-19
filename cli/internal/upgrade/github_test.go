package upgrade

import (
	"context"
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

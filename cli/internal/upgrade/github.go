package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

const repository = "catcursor/esp32-iphone-agent"

var platformName = regexp.MustCompile(`^[a-z0-9]+_[a-z0-9]+$`)

type githubRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// Firmware and CLI releases share a repository. Compare stable CLI tags across all pages.
func fetchGitHub(ctx context.Context, api, downloads string) (*Index, error) {
	var latest string
	for page := 1; ; page++ {
		resp, err := get(ctx, fmt.Sprintf("%s?per_page=100&page=%d", api, page))
		if err != nil {
			var status *httpStatusError
			if errors.As(err, &status) && (status.code == http.StatusForbidden || status.code == http.StatusTooManyRequests) {
				return fetchLatestGitHub(ctx, downloads)
			}
			return nil, err
		}
		var releases []githubRelease
		err = json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&releases)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("GitHub releases: %w", err)
		}
		for _, release := range releases {
			version, tagged := strings.CutPrefix(release.Tag, "cli-v")
			if !tagged || release.Draft || release.Prerelease {
				continue
			}
			if !stableVersion(version) {
				continue
			}
			if latest == "" || Compare(version, latest) > 0 {
				latest = version
			}
		}
		if !strings.Contains(resp.Header.Get("Link"), `rel="next"`) {
			break
		}
	}
	if latest == "" {
		return nil, errors.New("no published stable cli-v release in " + repository)
	}

	idx, err := fetchIndex(ctx, downloads+"/cli-v"+latest+"/versions.json")
	if err != nil {
		return nil, err
	}
	return checkGitHubIndex(idx, latest, downloads)
}

func stableVersion(version string) bool {
	v, ok := parse(version)
	return ok && fmt.Sprintf("%d.%d.%d", v.n[0], v.n[1], v.n[2]) == version
}

func fetchLatestGitHub(ctx context.Context, downloads string) (*Index, error) {
	url := strings.TrimSuffix(downloads, "/download") + "/latest/download/versions.json"
	resp, err := get(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("GitHub latest release: %w", err)
	}
	defer resp.Body.Close()
	idx, err := decodeIndex(resp.Body)
	if err != nil {
		return nil, err
	}
	if !stableVersion(idx.Latest) {
		return nil, fmt.Errorf("GitHub latest release has no stable CLI version: %q", idx.Latest)
	}
	// GitHub first resolves latest/download to a tag, then redirects to asset storage.
	// A firmware release may also include a file named versions.json.
	req := resp.Request
	for req.Response != nil && req.Response.Request.Response != nil {
		req = req.Response.Request
	}
	if req.Response == nil || req.URL.String() != downloads+"/cli-v"+idx.Latest+"/versions.json" {
		return nil, fmt.Errorf("GitHub latest release does not resolve to cli-v%s", idx.Latest)
	}
	return checkGitHubIndex(idx, idx.Latest, downloads)
}

func checkGitHubIndex(idx *Index, latest, downloads string) (*Index, error) {
	release, ok := idx.Find(latest)
	if !ok || idx.Latest != latest {
		return nil, fmt.Errorf("versions.json does not match GitHub tag cli-v%s", latest)
	}
	if len(release.Assets) == 0 {
		return nil, fmt.Errorf("no CLI assets in cli-v%s", latest)
	}
	base := downloads + "/cli-v" + latest + "/esp32agent_"
	for platform, asset := range release.Assets {
		if !platformName.MatchString(platform) {
			return nil, fmt.Errorf("unexpected CLI platform %q in cli-v%s", platform, latest)
		}
		ext := ".tar.gz"
		if strings.HasPrefix(platform, "windows_") {
			ext = ".zip"
		}
		if asset.URL != base+platform+ext {
			return nil, fmt.Errorf("unexpected asset URL for %s in cli-v%s", platform, latest)
		}
		if digest, err := hex.DecodeString(asset.SHA256); err != nil || len(digest) != sha256.Size {
			return nil, fmt.Errorf("invalid SHA-256 for %s in cli-v%s", platform, latest)
		}
	}
	return &Index{Latest: latest, Versions: []Release{release}}, nil
}

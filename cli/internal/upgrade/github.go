package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const repository = "catcursor/esp32-iphone-agent"

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
			v, ok := parse(version)
			if !ok || fmt.Sprintf("%d.%d.%d", v.n[0], v.n[1], v.n[2]) != version {
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

	base := downloads + "/cli-v" + latest + "/"
	idx, err := fetchIndex(ctx, base+"versions.json")
	if err != nil {
		return nil, err
	}
	release, ok := idx.Find(latest)
	if !ok || idx.Latest != latest {
		return nil, fmt.Errorf("versions.json does not match GitHub tag cli-v%s", latest)
	}
	for platform, asset := range release.Assets {
		extension := ".tar.gz"
		if strings.HasPrefix(platform, "windows_") {
			extension = ".zip"
		}
		if asset.URL != base+"esp32agent_"+platform+extension {
			return nil, fmt.Errorf("unexpected asset URL for %s in cli-v%s", platform, latest)
		}
	}
	return idx, nil
}

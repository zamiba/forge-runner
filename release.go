package runner

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// APIBase is where GitHub's API is reached. A variable so a test can stand a
// local server in for it.
var APIBase = "https://api.github.com"

// apiClient is for one small JSON request, not for a download: the install's own
// client carries the timeout that matters there.
var apiClient = &http.Client{Timeout: 15 * time.Second}

// LatestTag returns the newest non-prerelease tag for a release source.
//
// This is what stands in for the "latest release" that forge's fetch step
// deliberately has no notion of. A prerelease is skipped: a host's runner list
// has one button per runner, and a button that silently installs a pre-release
// is a support problem nobody asked for. GitHub's /releases/latest already
// excludes them, which is why it is used instead of listing releases and
// choosing.
func LatestTag(src ReleaseSource) (string, error) {
	if src.Host != "github" {
		return "", fmt.Errorf("unknown release host %q", src.Host)
	}
	if src.Repo == "" || strings.Count(src.Repo, "/") != 1 {
		return "", fmt.Errorf("release repo %q is not owner/name", src.Repo)
	}

	req, err := http.NewRequest("GET", APIBase+"/repos/"+src.Repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := apiClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", src.Repo, resp.Status)
	}

	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", err
	}
	if rel.TagName == "" {
		// A 200 with no tag would otherwise compose a download URL with an empty
		// path segment, which GitHub answers with something that is not the asset.
		return "", fmt.Errorf("%s: latest release has no tag", src.Repo)
	}
	return rel.TagName, nil
}

// Publisher is the account a release source belongs to, which is the field a
// SoftwareApplication's _itemTitle composes from.
func (s ReleaseSource) Publisher() string {
	owner, _, _ := strings.Cut(s.Repo, "/")
	return owner
}

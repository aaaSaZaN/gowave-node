package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// FetchRelease resolves an explicit tag, or GitHub's canonical latest stable
// release. Neither operation substitutes a different release for a missing tag.
func FetchRelease(repo, version string) (ReleaseInfo, error) {
	var release ReleaseInfo
	if repo == "" {
		repo = NodeRepository
	}
	if !repositoryPattern.MatchString(repo) {
		return release, fmt.Errorf("invalid repository")
	}
	if version == "" {
		return release, fmt.Errorf("release version is required")
	}
	suffix := "latest"
	if version != "latest" {
		suffix = "tags/" + url.PathEscape(version)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, err := fetchBytes(ctx, "https://api.github.com/repos/"+repo+"/releases/"+suffix, 4*1024*1024)
	if err != nil && version != "latest" && !strings.HasPrefix(version, "v") {
		data, err = fetchBytes(ctx, "https://api.github.com/repos/"+repo+"/releases/tags/"+url.PathEscape("v"+version), 4*1024*1024)
	}
	if err != nil {
		return release, err
	}
	if err = json.Unmarshal(data, &release); err != nil {
		return release, err
	}
	if release.TagName == "" || (version == "latest" && release.Prerelease) {
		return release, fmt.Errorf("no stable release found")
	}
	return release, nil
}

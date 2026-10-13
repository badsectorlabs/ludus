package ludusapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const githubReleaseMetadataLimit = 2 << 20

var githubRepositoryPart = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type githubReleaseSource struct {
	owner string
	repo  string
	tag   string
	asset string
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		State              string `json:"state"`
		Size               int64  `json:"size"`
		Digest             string `json:"digest"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func parseGitHubReleaseURL(raw string) (githubReleaseSource, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return githubReleaseSource{}, errors.New("enter an HTTPS GitHub repository or release URL")
	}
	path := strings.Trim(u.EscapedPath(), "/")
	parts := strings.Split(path, "/")
	for i, part := range parts {
		parts[i], err = url.PathUnescape(part)
		if err != nil || parts[i] == "" || strings.ContainsAny(parts[i], "/\\\x00\r\n") {
			return githubReleaseSource{}, errors.New("invalid GitHub release URL")
		}
	}
	if len(parts) < 2 || !githubRepositoryPart.MatchString(parts[0]) || !githubRepositoryPart.MatchString(parts[1]) || parts[0] == "." || parts[0] == ".." || parts[1] == "." || parts[1] == ".." {
		return githubReleaseSource{}, errors.New("invalid GitHub repository URL")
	}
	source := githubReleaseSource{owner: parts[0], repo: parts[1]}
	switch {
	case len(parts) == 2, len(parts) == 3 && parts[2] == "releases", len(parts) == 4 && parts[2] == "releases" && parts[3] == "latest":
		return source, nil
	case len(parts) == 5 && parts[2] == "releases" && parts[3] == "tag":
		source.tag = parts[4]
	case len(parts) == 6 && parts[2] == "releases" && parts[3] == "download":
		source.tag, source.asset = parts[4], parts[5]
	default:
		return githubReleaseSource{}, errors.New("use a GitHub repository, release, or release ZIP asset URL")
	}
	if len(source.tag) > 128 || source.tag == "." || source.tag == ".." || strings.ContainsAny(source.tag, "\x00\r\n") || len(source.asset) > 255 || (source.asset != "" && !strings.HasSuffix(strings.ToLower(source.asset), ".zip")) {
		return githubReleaseSource{}, errors.New("invalid GitHub release or ZIP asset URL")
	}
	return source, nil
}

func githubDownloadHost(host string) bool {
	return host == "github.com" || host == "release-assets.githubusercontent.com" || host == "objects.githubusercontent.com"
}

// Only GitHub's release asset hosts may be reached after a redirect. The
// transport is injectable so tests can exercise the actual request sequence.
func githubImportClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Timeout:   2 * time.Minute,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || req.URL.Scheme != "https" || req.URL.User != nil || !githubDownloadHost(req.URL.Host) {
				return errors.New("GitHub asset redirected outside approved download hosts")
			}
			return nil
		},
	}
}

func importGitHubPlugin(ctx context.Context, raw string, transport http.RoundTripper) ([]byte, int, error) {
	source, err := parseGitHubReleaseURL(raw)
	if err != nil {
		return nil, 400, err
	}
	client := githubImportClient(transport)
	api := "https://api.github.com/repos/" + source.owner + "/" + source.repo + "/releases/"
	if source.tag == "" {
		api += "latest"
	} else {
		api += "tags/" + url.PathEscape(source.tag)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return nil, 400, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "Ludus-Plugin-Installer")
	response, err := client.Do(request)
	if err != nil {
		return nil, 502, fmt.Errorf("could not read GitHub release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, 400, errors.New("GitHub release not found")
	}
	if response.StatusCode != http.StatusOK {
		return nil, 502, fmt.Errorf("GitHub release lookup failed (HTTP %d)", response.StatusCode)
	}
	metadata, err := io.ReadAll(io.LimitReader(response.Body, githubReleaseMetadataLimit+1))
	if err != nil || len(metadata) > githubReleaseMetadataLimit {
		return nil, 502, errors.New("GitHub release metadata is unavailable or too large")
	}
	var release githubRelease
	if err := json.Unmarshal(metadata, &release); err != nil || release.TagName == "" {
		return nil, 502, errors.New("invalid GitHub release metadata")
	}
	selected := -1
	for i, asset := range release.Assets {
		if asset.State != "uploaded" || !strings.HasSuffix(strings.ToLower(asset.Name), ".zip") || (source.asset != "" && asset.Name != source.asset) {
			continue
		}
		if selected != -1 {
			return nil, 400, errors.New("release has multiple ZIP assets; select a specific ZIP asset URL")
		}
		selected = i
	}
	if selected == -1 {
		return nil, 400, errors.New("this release has no compatible plugin ZIP")
	}
	asset := release.Assets[selected]
	if asset.Size < 1 || asset.Size > pluginUploadLimit {
		return nil, 413, errors.New("plugin package exceeds 128 MiB")
	}
	location, err := url.Parse(asset.BrowserDownloadURL)
	if err != nil || location.Scheme != "https" || location.Host != "github.com" || location.User != nil || location.RawQuery != "" || location.Fragment != "" || !strings.EqualFold(location.Path, "/"+source.owner+"/"+source.repo+"/releases/download/"+release.TagName+"/"+asset.Name) {
		return nil, 502, errors.New("GitHub returned an invalid release asset URL")
	}
	request, err = http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil)
	if err != nil {
		return nil, 502, err
	}
	request.Header.Set("User-Agent", "Ludus-Plugin-Installer")
	response, err = client.Do(request)
	if err != nil {
		return nil, 502, fmt.Errorf("could not download GitHub release asset: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, 502, fmt.Errorf("GitHub release asset download failed (HTTP %d)", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, pluginUploadLimit+1))
	if err != nil {
		return nil, 502, fmt.Errorf("could not read GitHub release asset: %w", err)
	}
	if len(data) > pluginUploadLimit {
		return nil, 413, errors.New("plugin package exceeds 128 MiB")
	}
	if asset.Digest != "" {
		sum := sha256.Sum256(data)
		digest := "sha256:" + hex.EncodeToString(sum[:])
		if !strings.EqualFold(digest, asset.Digest) {
			return nil, 502, errors.New("GitHub release asset checksum mismatch")
		}
	}
	return data, 200, nil
}

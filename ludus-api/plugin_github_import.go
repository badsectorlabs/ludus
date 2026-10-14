package ludusapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/go-github/v90/github"
)

const githubReleaseMetadataLimit = 2 << 20

var githubRepositoryPart = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type githubReleaseSource struct {
	owner string
	repo  string
	tag   string
	asset string
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

type githubImportTransport struct {
	next http.RoundTripper
}

func (t githubImportTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.User != nil || (req.URL.Host != "api.github.com" && !githubDownloadHost(req.URL.Host)) {
		return nil, errors.New("GitHub request outside approved hosts")
	}
	response, err := t.next.RoundTrip(req)
	if err == nil && response != nil && response.Body != nil && req.URL.Host == "api.github.com" && (strings.HasSuffix(req.URL.Path, "/releases/latest") || strings.Contains(req.URL.Path, "/releases/tags/")) {
		response.Body = struct {
			io.Reader
			io.Closer
		}{io.LimitReader(response.Body, githubReleaseMetadataLimit+1), response.Body}
	}
	return response, err
}

// Constrain both go-github's API requests and its release asset download.
// The transport is injectable so tests can exercise the actual request sequence.
func githubImportClient(transport http.RoundTripper) *http.Client {
	if transport == nil {
		transport = http.DefaultTransport
	}
	return &http.Client{
		Timeout:   2 * time.Minute,
		Transport: githubImportTransport{next: transport},
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
	httpClient := githubImportClient(transport)
	client, err := github.NewClient(github.WithHTTPClient(httpClient))
	if err != nil {
		return nil, 502, fmt.Errorf("configure GitHub client: %w", err)
	}
	var release *github.RepositoryRelease
	var response *github.Response
	if source.tag == "" {
		release, response, err = client.Repositories.GetLatestRelease(ctx, source.owner, source.repo)
	} else {
		release, response, err = client.Repositories.GetReleaseByTag(ctx, source.owner, source.repo, url.PathEscape(source.tag))
	}
	if err != nil {
		if response != nil && response.StatusCode == http.StatusNotFound {
			return nil, 400, errors.New("GitHub release not found")
		}
		return nil, 502, fmt.Errorf("could not read GitHub release: %w", err)
	}
	if release == nil || release.TagName == "" {
		return nil, 502, errors.New("invalid GitHub release metadata")
	}
	var selected *github.ReleaseAsset
	for _, asset := range release.Assets {
		if asset.GetState() != "uploaded" || !strings.HasSuffix(strings.ToLower(asset.GetName()), ".zip") || (source.asset != "" && asset.GetName() != source.asset) {
			continue
		}
		if selected != nil {
			return nil, 400, errors.New("release has multiple ZIP assets; select a specific ZIP asset URL")
		}
		selected = asset
	}
	if selected == nil {
		return nil, 400, errors.New("this release has no compatible plugin ZIP")
	}
	if selected.GetSize() < 1 || selected.GetSize() > pluginUploadLimit {
		return nil, 413, errors.New("plugin package exceeds 128 MiB")
	}
	location, err := url.Parse(selected.GetBrowserDownloadURL())
	if err != nil || location.Scheme != "https" || location.Host != "github.com" || location.User != nil || location.RawQuery != "" || location.Fragment != "" || !strings.EqualFold(location.Path, "/"+source.owner+"/"+source.repo+"/releases/download/"+release.TagName+"/"+selected.GetName()) {
		return nil, 502, errors.New("GitHub returned an invalid release asset URL")
	}
	if selected.ID == nil || selected.GetID() <= 0 {
		return nil, 502, errors.New("GitHub release asset has no ID")
	}
	body, redirect, err := client.Repositories.DownloadReleaseAsset(ctx, source.owner, source.repo, selected.GetID(), httpClient)
	if err != nil {
		return nil, 502, fmt.Errorf("could not download GitHub release asset: %w", err)
	}
	if body == nil || redirect != "" {
		return nil, 502, errors.New("GitHub release asset download did not return a package")
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, pluginUploadLimit+1))
	if err != nil {
		return nil, 502, fmt.Errorf("could not read GitHub release asset: %w", err)
	}
	if len(data) > pluginUploadLimit {
		return nil, 413, errors.New("plugin package exceeds 128 MiB")
	}
	if selected.GetDigest() != "" {
		sum := sha256.Sum256(data)
		digest := "sha256:" + hex.EncodeToString(sum[:])
		if !strings.EqualFold(digest, selected.GetDigest()) {
			return nil, 502, errors.New("GitHub release asset checksum mismatch")
		}
	}
	return data, 200, nil
}

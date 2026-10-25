package ludusapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
	"io"
	"net/http"
	"strings"
	"testing"
)

type githubRoundTripFunc func(*http.Request) (*http.Response, error)

func (f githubRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func githubTestResponse(status int, body []byte, location string) *http.Response {
	header := http.Header{}
	if location != "" {
		header.Set("Location", location)
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(bytes.NewReader(body))}
}

func githubFixtureTransport(t *testing.T, pkg []byte, assetName string) http.RoundTripper {
	t.Helper()
	sum := sha256.Sum256(pkg)
	assetURL := "https://github.com/example/plugin/releases/download/v1.0.0/" + assetName
	metadata, _ := json.Marshal(map[string]any{"tag_name": "v1.0.0", "assets": []map[string]any{{"id": 42, "name": assetName, "state": "uploaded", "size": len(pkg), "digest": "sha256:" + hex.EncodeToString(sum[:]), "browser_download_url": assetURL}}})
	return githubRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Host == "api.github.com" && request.URL.Path == "/repos/example/plugin/releases/latest":
			return githubTestResponse(200, metadata, ""), nil
		case request.URL.Host == "api.github.com" && request.URL.Path == "/repos/example/plugin/releases/tags/v1.0.0":
			return githubTestResponse(200, metadata, ""), nil
		case request.URL.Host == "api.github.com" && request.URL.Path == "/repos/example/plugin/releases/assets/42":
			return githubTestResponse(302, nil, "https://release-assets.githubusercontent.com/download/asset"), nil
		case request.URL.String() == "https://release-assets.githubusercontent.com/download/asset":
			return githubTestResponse(200, pkg, ""), nil
		default:
			t.Errorf("unexpected GitHub request: %s", request.URL)
			return githubTestResponse(404, nil, ""), nil
		}
	})
}

func TestGitHubReleaseURLBoundaries(t *testing.T) {
	for _, raw := range []string{
		"https://github.com/example/plugin", "https://github.com/example/plugin/releases",
		"https://github.com/example/plugin/releases/latest", "https://github.com/example/plugin/releases/tag/v1.0.0",
		"https://github.com/example/plugin/releases/download/v1.0.0/plugin.zip",
	} {
		if _, err := parseGitHubReleaseURL(raw); err != nil {
			t.Errorf("valid URL %s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		"http://github.com/example/plugin", "https://github.com.evil.test/example/plugin",
		"https://github.com:443/example/plugin", "https://user@github.com/example/plugin",
		"https://github.com/example/plugin?redirect=https://internal.test",
		"https://github.com/example/plugin/releases/download/v1.0.0/plugin.tar.gz",
		"https://github.com/example/plugin/releases/tag/%2Fadmin",
		"https://github.com/example/plugin/../../internal", "https://github.com/example/plugin/tree/main",
	} {
		if _, err := parseGitHubReleaseURL(raw); err == nil {
			t.Errorf("unsafe URL accepted: %s", raw)
		}
	}
}

func TestGitHubReleaseImportAndRedirectBoundary(t *testing.T) {
	pkg := testPackage(t, protocol.Manifest{SchemaVersion: 1, ID: "github-plugin", Name: "GitHub Plugin", Version: "1", UI: "ui/index.html"}, map[string]string{"ui/index.html": "<h1>GitHub</h1>"})
	for _, raw := range []string{
		"https://github.com/example/plugin",
		"https://github.com/example/plugin/releases/tag/v1.0.0",
		"https://github.com/example/plugin/releases/download/v1.0.0/plugin.zip",
	} {
		data, status, err := importGitHubPlugin(context.Background(), raw, githubFixtureTransport(t, pkg, "plugin.zip"))
		if err != nil || status != 200 || !bytes.Equal(data, pkg) {
			t.Fatalf("%s: status %d, error %v", raw, status, err)
		}
	}
	blocked := githubRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "api.github.com" && request.URL.Path != "/repos/example/plugin/releases/assets/42" {
			return githubFixtureTransport(t, pkg, "plugin.zip").RoundTrip(request)
		}
		if request.URL.Host == "api.github.com" {
			return githubTestResponse(302, nil, "http://127.0.0.1/private"), nil
		}
		t.Fatalf("followed unsafe redirect to %s", request.URL)
		return nil, nil
	})
	if _, status, err := importGitHubPlugin(context.Background(), "https://github.com/example/plugin", blocked); status != 502 || err == nil {
		t.Fatalf("unsafe redirect: status %d, error %v", status, err)
	}
}

func TestGitHubReleaseNotFound(t *testing.T) {
	missing := githubRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return githubTestResponse(http.StatusNotFound, []byte(`{"message":"Not Found"}`), ""), nil
	})
	if _, status, err := importGitHubPlugin(context.Background(), "https://github.com/example/plugin", missing); status != 400 || err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing release: status %d, error %v", status, err)
	}
}

func TestGitHubImportRejectsAmbiguousAndChangedAssets(t *testing.T) {
	pkg := []byte("test package")
	makeTransport := func(assets []map[string]any, download []byte) http.RoundTripper {
		metadata, _ := json.Marshal(map[string]any{"tag_name": "v1.0.0", "assets": assets})
		return githubRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Host == "api.github.com" && request.URL.Path != "/repos/example/plugin/releases/assets/42" {
				return githubTestResponse(200, metadata, ""), nil
			}
			return githubTestResponse(200, download, ""), nil
		})
	}
	asset := map[string]any{"id": 42, "name": "plugin.zip", "state": "uploaded", "size": len(pkg), "browser_download_url": "https://github.com/example/plugin/releases/download/v1.0.0/plugin.zip"}
	other := map[string]any{"id": 43, "name": "other.zip", "state": "uploaded", "size": len(pkg), "browser_download_url": "https://github.com/example/plugin/releases/download/v1.0.0/other.zip"}
	if _, status, err := importGitHubPlugin(context.Background(), "https://github.com/example/plugin", makeTransport([]map[string]any{asset, other}, pkg)); status != 400 || err == nil || !strings.Contains(err.Error(), "multiple ZIP") {
		t.Fatalf("ambiguous release: status %d, error %v", status, err)
	}
	if _, status, err := importGitHubPlugin(context.Background(), "https://github.com/example/plugin/releases/download/v1.0.0/plugin.zip", makeTransport([]map[string]any{asset, other}, pkg)); status != 200 || err != nil {
		t.Fatalf("explicit asset: status %d, error %v", status, err)
	}
	asset["digest"] = "sha256:" + strings.Repeat("0", 64)
	if _, status, err := importGitHubPlugin(context.Background(), "https://github.com/example/plugin", makeTransport([]map[string]any{asset}, pkg)); status != 502 || err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("changed asset: status %d, error %v", status, err)
	}
	asset["size"] = pluginUploadLimit + 1
	if _, status, err := importGitHubPlugin(context.Background(), "https://github.com/example/plugin", makeTransport([]map[string]any{asset}, pkg)); status != 413 || err == nil {
		t.Fatalf("oversized asset: status %d, error %v", status, err)
	}
}

package ludusapi

import (
	"bytes"
	"encoding/json"
	"ludusapi/dto"
	"ludusapi/models"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

func TestBuildCollectionInstallArg(t *testing.T) {
	tests := []struct {
		name       string
		collection string
		version    string
		want       string
	}{
		{
			name:       "HTTP archive",
			collection: "http://127.0.0.1:8000/ludus_sccm-1.0.6.tar.gz",
			want:       "http://127.0.0.1:8000/ludus_sccm-1.0.6.tar.gz",
		},
		{
			name:       "HTTPS archive with query and fragment",
			collection: "https://example.com/ns-collection-1.0.0.tar.gz?token=abc#download",
			want:       "https://example.com/ns-collection-1.0.0.tar.gz?token=abc#download",
		},
		{
			name:       "archive version is encoded in artifact",
			collection: "https://example.com/ns-collection-1.0.0.tar.gz",
			version:    "1.0.0",
			want:       "https://example.com/ns-collection-1.0.0.tar.gz",
		},
		{
			name:       "bare HTTPS Git repository",
			collection: "https://example.com/ns/collection.git",
			want:       "git+https://example.com/ns/collection.git",
		},
		{
			name:       "bare Git URL without suffix and with ref",
			collection: "https://example.com/ns/collection",
			version:    "devel",
			want:       "git+https://example.com/ns/collection,devel",
		},
		{
			name:       "explicit Git overrides archive suffix",
			collection: "git+https://example.com/collection.tar.gz",
			version:    "v1.0.0",
			want:       "git+https://example.com/collection.tar.gz,v1.0.0",
		},
		{
			name:       "SSH Git URL",
			collection: "ssh://git@example.com/ns/collection.git",
			version:    "main",
			want:       "git+ssh://git@example.com/ns/collection.git,main",
		},
		{
			name:       "SCP Git source",
			collection: "git@example.com:ns/collection.git",
			version:    "main",
			want:       "git@example.com:ns/collection.git,main",
		},
		{
			name:       "Galaxy collection",
			collection: "community.windows",
			want:       "community.windows",
		},
		{
			name:       "Galaxy version pin",
			collection: "community.windows",
			version:    "3.0.0",
			want:       "community.windows:==3.0.0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := buildCollectionInstallArg(test.collection, test.version); got != test.want {
				t.Errorf("buildCollectionInstallArg(%q, %q) = %q, want %q", test.collection, test.version, got, test.want)
			}
		})
	}
}

func TestActionCollectionFromInternetArchiveQuery(t *testing.T) {
	if _, err := exec.LookPath("ansible-galaxy"); err != nil {
		t.Skip("requires ansible-galaxy")
	}

	root := t.TempDir()
	collections := filepath.Join(root, "collections")
	t.Setenv("ANSIBLE_HOME", filepath.Join(root, "home"))
	t.Setenv("ANSIBLE_LOCAL_TEMP", filepath.Join(root, "tmp"))
	t.Setenv("ANSIBLE_COLLECTIONS_PATH", collections)
	t.Setenv("ANSIBLE_FORCE_COLOR", "false")

	server := httptest.NewServer(http.FileServer(http.Dir("../ludus-server/ci/fixtures")))
	t.Cleanup(server.Close)
	body, err := json.Marshal(dto.InstallCollectionRequest{
		Collection: server.URL + "/ludus_ci-http_archive-1.0.0.tar.gz?token=ci",
		Force:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/ansible/collection", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	event := &core.RequestEvent{Event: router.Event{Request: request, Response: response}}
	user := &models.User{}
	user.SetProxyRecord(core.NewRecord(core.NewBaseCollection("users")))
	user.SetIsAdmin(true)
	// Empty username keeps installation paths in the isolated Ansible environment.
	event.Set("user", user)

	if err := ActionCollectionFromInternet(event); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusCreated {
		t.Fatalf("collection install returned HTTP %d: %s", response.Code, response.Body.String())
	}

	data, err := os.ReadFile(filepath.Join(collections, "ansible_collections", "ludus_ci", "http_archive", "MANIFEST.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		CollectionInfo struct {
			Namespace string
			Name      string
			Version   string
		} `json:"collection_info"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	info := manifest.CollectionInfo
	if info.Namespace != "ludus_ci" || info.Name != "http_archive" || info.Version != "1.0.0" {
		t.Fatalf("unexpected installed collection: %+v", info)
	}
}

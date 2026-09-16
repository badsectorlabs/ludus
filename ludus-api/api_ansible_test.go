package ludusapi

import "testing"

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

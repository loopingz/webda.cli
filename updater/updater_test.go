package updater

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNeedsUpdate_Satisfied(t *testing.T) {
	needs, err := NeedsUpdate("1.2.3", ">=1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if needs {
		t.Error("1.2.3 satisfies >=1.0.0, should not need update")
	}
}

func TestNeedsUpdate_NotSatisfied(t *testing.T) {
	needs, err := NeedsUpdate("1.0.0", ">=1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if !needs {
		t.Error("1.0.0 does not satisfy >=1.2.0, should need update")
	}
}

func TestNeedsUpdate_DevVersion(t *testing.T) {
	needs, err := NeedsUpdate("dev", ">=1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if needs {
		t.Error("dev version should skip update check")
	}
}

func TestNeedsUpdate_EmptyConstraint(t *testing.T) {
	needs, err := NeedsUpdate("1.0.0", "")
	if err != nil {
		t.Fatal(err)
	}
	if needs {
		t.Error("empty constraint should not need update")
	}
}

func TestNeedsUpdate_ComplexConstraint(t *testing.T) {
	needs, err := NeedsUpdate("2.0.0", ">=1.0.0, <2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if !needs {
		t.Error("2.0.0 does not satisfy >=1.0.0, <2.0.0")
	}
}

func TestAssetName(t *testing.T) {
	tests := []struct {
		goos, goarch, want string
	}{
		{"linux", "amd64", "webda-cli-linux-amd64"},
		{"darwin", "arm64", "webda-cli-darwin-arm64"},
		{"windows", "amd64", "webda-cli-windows-amd64.exe"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := AssetName(tt.goos, tt.goarch)
			if got != tt.want {
				t.Errorf("AssetName(%q, %q) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
			}
		})
	}
}

func TestFetchLatestRelease_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(GHRelease{
			TagName: "v1.3.0",
			Assets: []GHAsset{
				{Name: "webda-cli-linux-amd64", BrowserDownloadURL: "https://example.com/linux"},
				{Name: "webda-cli-darwin-arm64", BrowserDownloadURL: "https://example.com/darwin"},
			},
		})
	}))
	defer srv.Close()

	rel, err := FetchLatestRelease(srv.URL + "/")
	if err != nil {
		t.Fatalf("FetchLatestRelease failed: %v", err)
	}
	if rel.TagName != "v1.3.0" {
		t.Errorf("expected tag v1.3.0, got %q", rel.TagName)
	}
	if len(rel.Assets) != 2 {
		t.Errorf("expected 2 assets, got %d", len(rel.Assets))
	}
}

func TestFetchLatestRelease_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := FetchLatestRelease(srv.URL + "/")
	if err == nil {
		t.Fatal("expected error for server error")
	}
}

func TestFindAssetURL_Found(t *testing.T) {
	rel := &GHRelease{
		TagName: "v1.3.0",
		Assets: []GHAsset{
			{Name: AssetName("linux", "amd64"), BrowserDownloadURL: "https://example.com/linux-amd64"},
			{Name: AssetName("darwin", "arm64"), BrowserDownloadURL: "https://example.com/darwin-arm64"},
		},
	}
	url, tag, err := FindAssetURL(rel)
	if err != nil {
		t.Fatalf("FindAssetURL failed: %v", err)
	}
	if tag != "v1.3.0" {
		t.Errorf("expected tag v1.3.0, got %q", tag)
	}
	if url == "" {
		t.Error("expected non-empty URL")
	}
}

func TestFindAssetURL_NotFound(t *testing.T) {
	rel := &GHRelease{
		TagName: "v1.3.0",
		Assets:  []GHAsset{{Name: "webda-cli-plan9-mips", BrowserDownloadURL: "https://example.com/plan9"}},
	}
	_, _, err := FindAssetURL(rel)
	if err == nil {
		t.Fatal("expected error when asset not found")
	}
}

func TestDownloadAndReplace_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	err := DownloadAndReplace(srv.URL + "/binary")
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

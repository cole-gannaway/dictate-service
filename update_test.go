package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSelectAsset(t *testing.T) {
	assets := []releaseAsset{
		{Name: "dictate-darwin-arm64", DownloadURL: "https://example.invalid/darwin-arm64"},
		{Name: "dictate-linux-amd64", DownloadURL: "https://example.invalid/linux-amd64"},
	}

	got, err := selectAsset(assets, "darwin", "arm64")
	if err != nil {
		t.Fatalf("selectAsset() error = %v", err)
	}
	if got.DownloadURL != "https://example.invalid/darwin-arm64" {
		t.Fatalf("DownloadURL = %q, want the darwin/arm64 asset", got.DownloadURL)
	}

	if _, err := selectAsset(assets, "windows", "amd64"); err == nil {
		t.Fatal("expected an error for a platform with no matching asset")
	}
}

func TestFetchLatestRelease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name": "1.2.0", "assets": [{"name": "dictate-linux-amd64", "browser_download_url": "https://example.invalid/x"}]}`))
	}))
	defer server.Close()

	release, err := fetchLatestRelease(server.URL)
	if err != nil {
		t.Fatalf("fetchLatestRelease() error = %v", err)
	}
	if release.TagName != "1.2.0" {
		t.Fatalf("TagName = %q, want %q", release.TagName, "1.2.0")
	}
	if len(release.Assets) != 1 || release.Assets[0].Name != "dictate-linux-amd64" {
		t.Fatalf("Assets = %+v, want one dictate-linux-amd64 asset", release.Assets)
	}
}

func TestFetchLatestReleaseErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	if _, err := fetchLatestRelease(server.URL); err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
}

func TestDownloadToWritesExecutableFileAtomically(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fake binary contents"))
	}))
	defer server.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "dictate")

	if err := downloadTo(server.URL, dest); err != nil {
		t.Fatalf("downloadTo() error = %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading dest: %v", err)
	}
	if string(got) != "fake binary contents" {
		t.Fatalf("dest contents = %q, want %q", got, "fake binary contents")
	}

	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("dest mode = %v, want executable bit set", info.Mode())
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("dir entries = %v, want only the final dest file (no leftover temp file)", entries)
	}
}

func TestDownloadToErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusGone)
	}))
	defer server.Close()

	dir := t.TempDir()
	if err := downloadTo(server.URL, filepath.Join(dir, "dictate")); err == nil {
		t.Fatal("expected an error for a non-200 download response")
	}
}

// TestRunUpdateAlreadyUpToDate is the one runUpdate case safe to exercise
// directly: it returns before ever calling os.Executable(), so it can't
// touch the real test binary the way the download path would.
func TestRunUpdateAlreadyUpToDate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"tag_name": "1.2.0", "assets": []}`))
	}))
	defer server.Close()

	if err := runUpdate("1.2.0", server.URL); err != nil {
		t.Fatalf("runUpdate() error = %v", err)
	}
}

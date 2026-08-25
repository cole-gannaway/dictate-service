package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const releaseRepo = "cole-gannaway/dictate-service"

const githubLatestReleaseURL = "https://api.github.com/repos/" + releaseRepo + "/releases/latest"

type releaseAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

type githubRelease struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

// runUpdate checks latestReleaseURL for the newest published release and,
// if its tag differs from currentVersion, downloads the asset matching
// this platform and replaces the running executable with it in place.
func runUpdate(currentVersion, latestReleaseURL string) error {
	release, err := fetchLatestRelease(latestReleaseURL)
	if err != nil {
		return fmt.Errorf("checking latest release: %w", err)
	}

	if release.TagName == currentVersion {
		fmt.Printf("already up to date (%s)\n", currentVersion)
		return nil
	}

	asset, err := selectAsset(release.Assets, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return fmt.Errorf("release %s: %w", release.TagName, err)
	}

	// Resolve symlinks (e.g. a PATH entry pointing at dist/dictate) so we
	// replace the real binary, not the symlink.
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locating current executable: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolving current executable: %w", err)
	}

	fmt.Printf("updating %s -> %s ...\n", currentVersion, release.TagName)
	if err := downloadTo(asset.DownloadURL, exePath); err != nil {
		return fmt.Errorf("downloading %s: %w", asset.Name, err)
	}
	fmt.Printf("updated to %s -- restart dictate to run it\n", release.TagName)
	return nil
}

// selectAsset finds the release asset built for the given platform.
func selectAsset(assets []releaseAsset, goos, goarch string) (releaseAsset, error) {
	want := fmt.Sprintf("dictate-%s-%s", goos, goarch)
	for _, a := range assets {
		if a.Name == want {
			return a, nil
		}
	}
	return releaseAsset{}, fmt.Errorf("no release asset named %q", want)
}

func fetchLatestRelease(url string) (*githubRelease, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s: %s", resp.Status, body)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	return &release, nil
}

// downloadTo fetches url and replaces dest with the result. The download is
// written to a temp file in dest's directory first and moved into place
// with os.Rename, which is atomic on the same filesystem -- so a failed or
// interrupted download never leaves dest half-written, and replacing the
// currently-running executable is safe (the OS keeps serving the old inode
// to the running process until it exits).
func downloadTo(url, dest string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, body)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dest), ".dictate-update-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return err
	}
	return os.Rename(tmpPath, dest)
}

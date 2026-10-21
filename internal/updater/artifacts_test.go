package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssetSelectionRejectsWrongProductOSAndChecksums(t *testing.T) {
	for _, assets := range [][]ReleaseAsset{
		{{Name: "gowave-node-linux-amd64.sha256"}}, {{Name: "unrelated-linux-amd64"}}, {{Name: "gowave-node-windows-amd64"}}, {{Name: "gowave-node-linux-arm64"}},
	} {
		if asset, err := MatchAsset(assets, "linux", "amd64", "gowave-node"); err == nil {
			t.Fatalf("accepted %s", asset.Name)
		}
	}
}
func TestChecksums(t *testing.T) {
	sum := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, line := range []string{sum + "  binary", sum + " *binary", "SHA2-256= " + sum} {
		digest, err := checksumFromManifest([]byte(line), "binary")
		if err != nil || digest != sum {
			t.Fatal(digest, err)
		}
	}
	if _, err := checksumFromManifest([]byte(sum+"  other"), "binary"); err == nil {
		t.Fatal("accepted checksum of another asset")
	}
}
func TestCoreResetRetainsStockAndRollback(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "rw-core")
	candidate := filepath.Join(dir, "candidate")
	os.WriteFile(target, []byte("stock"), 0700)
	os.WriteFile(candidate, []byte("custom"), 0700)
	if err := ActivateCore(candidate, target); err != nil {
		t.Fatal(err)
	}
	if err := ResetCore(target); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(target)
	if string(content) != "stock" {
		t.Fatal(string(content))
	}
	if err := Rollback(target); err != nil {
		t.Fatal(err)
	}
	content, _ = os.ReadFile(target)
	if string(content) != "custom" {
		t.Fatal("rollback did not restore preceding core")
	}
	content, _ = os.ReadFile(target + ".stock")
	if string(content) != "stock" {
		t.Fatal("stock artifact consumed")
	}
}

func TestStockUpgradeRemainsDefault(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "rw-core")
	candidate := filepath.Join(dir, "new-core")
	os.WriteFile(target, []byte("old"), 0700)
	os.WriteFile(target+".stock", []byte("old"), 0700)
	os.WriteFile(candidate, []byte("new"), 0700)
	if err := InstallStockCore(candidate, target); err != nil {
		t.Fatal(err)
	}
	path, err := PrepareCore(context.Background(), target, nil)
	if err != nil || path != target {
		t.Fatalf("stock upgrade would be reverted: %s %v", path, err)
	}
	if err := Rollback(target); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{target, target + ".stock"} {
		data, _ := os.ReadFile(file)
		if string(data) != "old" {
			t.Fatalf("%s: %s", file, data)
		}
	}
}
func TestVerifiedRawDownloadIsExecutable(t *testing.T) {
	content := []byte("#!/bin/sh\necho 'gowave-node vtest'\n")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(content) }))
	defer server.Close()
	previous := HTTPClient
	HTTPClient = server.Client()
	defer func() { HTTPClient = previous }()
	sum := sha256.Sum256(content)
	asset := ReleaseAsset{Name: "gowave-node-linux-amd64", BrowserDownloadURL: server.URL + "/binary", Digest: "sha256:" + hex.EncodeToString(sum[:])}
	path, err := DownloadReleaseBinary(ReleaseInfo{}, asset, "gowave-node")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if err := ValidateNodeBinary(path); err != nil {
		t.Fatal(err)
	}
	asset.Digest = "sha256:" + strings.Repeat("0", 64)
	if path, err := DownloadReleaseBinary(ReleaseInfo{}, asset, "gowave-node"); err == nil {
		os.Remove(path)
		t.Fatal("accepted mismatched digest")
	}
}

func TestUpdateLockExcludesConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "binary")
	unlock, err := LockTarget(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := LockTarget(path); err == nil {
		second()
		t.Fatal("concurrent update lock accepted")
	}
	unlock()
	third, err := LockTarget(path)
	if err != nil {
		t.Fatal(err)
	}
	third()
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestReleaseSelectionNeverSubstitutesMissingVersion(t *testing.T) {
	previous := HTTPClient
	defer func() { HTTPClient = previous }()
	var paths []string
	HTTPClient = &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		paths = append(paths, r.URL.Path)
		status := 404
		body := `{"message":"not found"}`
		if strings.HasSuffix(r.URL.Path, "/latest") {
			status = 200
			body = `{"tag_name":"v1.2.3","prerelease":false}`
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	if _, err := FetchRelease(NodeRepository, "missing"); err == nil {
		t.Fatal("missing release accepted")
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "/latest") {
			t.Fatal("missing version fell back to latest")
		}
	}
	release, err := FetchRelease(NodeRepository, "latest")
	if err != nil || release.TagName != "v1.2.3" {
		t.Fatal(release, err)
	}
}

func TestStockUpgradeCopyFailureClearsPendingMarker(t *testing.T) {
	target := filepath.Join(t.TempDir(), "rw-core")
	if err := os.WriteFile(target, []byte("old"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := InstallStockCore(target+".missing", target); err == nil {
		t.Fatal("missing candidate accepted")
	}
	if _, err := os.Stat(target + ".stock.pending"); !os.IsNotExist(err) {
		t.Fatalf("pending marker remains: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "old" {
		t.Fatalf("active core changed: %q %v", data, err)
	}
}

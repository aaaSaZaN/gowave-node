package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

type ReleaseAsset struct {
	Name               string `json:"name"`
	Digest             string `json:"digest"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type ReleaseInfo struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	Prerelease  bool           `json:"prerelease"`
	PublishedAt time.Time      `json:"published_at"`
	Assets      []ReleaseAsset `json:"assets"`
}

type CheckResult struct {
	CurrentVersion   string        `json:"currentVersion"`
	LatestRelease    *ReleaseInfo  `json:"latestRelease,omitempty"`
	LatestPrerelease *ReleaseInfo  `json:"latestPrerelease,omitempty"`
	AllReleases      []ReleaseInfo `json:"allReleases"`
	Architecture     string        `json:"architecture"`
	OS               string        `json:"os"`
	CurrentCoreVer   string        `json:"currentCoreVersion"`
}

func FetchReleases(repo string) ([]ReleaseInfo, error) {
	if repo == "" {
		repo = NodeRepository
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(repo) {
		return nil, fmt.Errorf("invalid repository")
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=100", repo)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gowave-node-updater")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api returned status %d", resp.StatusCode)
	}

	var releases []ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	for i := range releases {
		lower := strings.ToLower(releases[i].TagName)
		if releases[i].Prerelease || strings.Contains(lower, "alpha") || strings.Contains(lower, "beta") || strings.Contains(lower, "rc") || strings.Contains(lower, "pre") || strings.Contains(lower, "dev") {
			releases[i].Prerelease = true
		}
	}
	return releases, nil
}

func MatchAsset(assets []ReleaseAsset, targetOS, targetArch, binaryName string) (*ReleaseAsset, error) {
	names := []string{}
	if binaryName == "gowave-node" {
		arch := targetArch
		if arch == "arm" {
			arch = "armv7"
		}
		names = []string{"gowave-node-" + targetOS + "-" + arch, "gowave-node-" + targetOS + "-" + arch + ".tar.gz", "gowave-node-" + targetOS + "-" + arch + ".zip"}
	}
	if binaryName == "rw-core" && targetOS == "linux" {
		arch := map[string]string{"amd64": "64", "386": "32", "arm": "arm32-v7a", "armv5": "arm32-v5", "armv6": "arm32-v6", "armv7": "arm32-v7a", "arm64": "arm64-v8a", "mips": "mips32", "mipsle": "mips32le", "mips64": "mips64", "mips64le": "mips64le", "riscv64": "riscv64"}[targetArch]
		if arch != "" {
			names = []string{"Xray-linux-" + arch + ".zip"}
		}
	}
	if binaryName == "geocheck" {
		names = []string{"geocheck_" + targetOS + "_" + targetArch + ".tar.gz"}
	}
	for _, name := range names {
		for _, asset := range assets {
			if asset.Name == name {
				a := asset
				return &a, nil
			}
		}
	}
	return nil, fmt.Errorf("no matching %s asset for %s/%s", binaryName, targetOS, targetArch)
}

func DownloadAndSaveBinary(downloadURL, targetBinaryName string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	data, err := fetchBytes(ctx, downloadURL, MaxArtifactSize)
	if err != nil {
		return "", err
	}
	return saveBinary(data, downloadURL, targetBinaryName)
}
func saveBinary(data []byte, source, name string) (string, error) {
	f, err := os.CreateTemp("", name+"-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	f.Close()
	lower := strings.ToLower(source)
	if strings.HasSuffix(lower, ".zip") {
		os.Remove(path)
		return extractZip(data, name, path)
	}
	if strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") {
		os.Remove(path)
		return extractTarGz(data, name, path)
	}
	if err = os.WriteFile(path, data, 0700); err != nil {
		os.Remove(path)
		return "", err
	}
	if err = os.Chmod(path, 0700); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func GetStandardAssetDirs(extraDirs ...string) []string {
	dirs := []string{"/usr/local/share/xray", "/opt/gowave-node", "/usr/share/xray"}
	if exe, err := os.Executable(); err == nil {
		dirs = append([]string{filepath.Dir(exe)}, dirs...)
	}
	for _, ed := range extraDirs {
		if ed != "" {
			dirs = append(dirs, ed)
		}
	}
	seen := make(map[string]bool)
	res := make([]string, 0, len(dirs))
	for _, d := range dirs {
		clean := filepath.Clean(d)
		if !seen[clean] {
			seen[clean] = true
			res = append(res, clean)
		}
	}
	return res
}

func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func extractZip(data []byte, binaryName, destPath string) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if base != binaryName && !(binaryName == "rw-core" && base == "xray") {
			continue
		}
		if f.UncompressedSize64 > uint64(MaxArtifactSize) {
			return "", fmt.Errorf("archive executable too large")
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		return extractExecutable(rc, destPath)
	}
	return "", fmt.Errorf("binary %s not found in zip", binaryName)
}
func extractTarGz(data []byte, binaryName, destPath string) (string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		base := filepath.Base(header.Name)
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if base == binaryName || (binaryName == "rw-core" && base == "xray") {
			if header.Size > MaxArtifactSize {
				return "", fmt.Errorf("archive executable too large")
			}
			return extractExecutable(tr, destPath)
		}
	}
	return "", fmt.Errorf("binary %s not found in archive", binaryName)
}
func extractExecutable(r io.Reader, dest string) (string, error) {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return "", err
	}
	size, err := io.Copy(f, io.LimitReader(r, MaxArtifactSize+1))
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if size == 0 || size > MaxArtifactSize {
		err = fmt.Errorf("empty or oversized executable")
	}
	if err != nil {
		os.Remove(dest)
		return "", err
	}
	return dest, nil
}

func AtomicReplace(tmpBinaryPath, targetPath string) error {
	in, err := os.Open(tmpBinaryPath)
	if err != nil {
		return err
	}
	defer in.Close()
	f, err := os.CreateTemp(filepath.Dir(targetPath), ".update-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = io.Copy(f, in)
	if err == nil {
		err = f.Chmod(0755)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), targetPath); err != nil {
		return err
	}
	_ = os.Remove(tmpBinaryPath)
	return nil
}
func binaryOutput(path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, path, args...).CombinedOutput()
}

func ValidateCoreBinary(corePath string) error {
	out, err := binaryOutput(corePath, "version")
	if err != nil {
		return fmt.Errorf("core validation: %w", err)
	}
	if !strings.HasPrefix(string(out), "Xray ") {
		return fmt.Errorf("download is not an Xray core")
	}
	return nil
}

func GetCurrentArch() string {
	return runtime.GOARCH
}

// AssetArch names release assets for this build. Unlike GOARCH it keeps the ARM
// variant, so an armv5/armv6 node never downloads an armv7 node or core.
func AssetArch() string {
	if runtime.GOARCH != "arm" {
		return runtime.GOARCH
	}
	return "armv" + armVersion()
}

func armVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			// GOARM may carry a float ABI suffix, such as "6,softfloat".
			if setting.Key == "GOARM" && setting.Value != "" {
				return strings.SplitN(setting.Value, ",", 2)[0]
			}
		}
	}
	return "7"
}

func GetCurrentOS() string {
	return runtime.GOOS
}

func EnsureCore(targetPath string) (string, error) {
	if targetPath != "" {
		if fi, err := os.Stat(targetPath); err == nil && !fi.IsDir() {
			if err := EnsureGeodata(filepath.Dir(targetPath)); err != nil {
				return "", err
			}
			return targetPath, nil
		}
	}

	repo := CoreRepository
	fmt.Printf("[CORE] Xray core not found. Automatically downloading latest release from %s...\n", repo)

	release, err := FetchRelease(repo, "latest")
	if err != nil {
		return "", fmt.Errorf("failed to fetch latest stable core: %w", err)
	}
	latest := &release

	asset, err := MatchAsset(latest.Assets, GetCurrentOS(), AssetArch(), "rw-core")
	if err != nil {
		return "", fmt.Errorf("could not find compatible asset for %s/%s: %w", GetCurrentOS(), AssetArch(), err)
	}

	fmt.Printf("[CORE] Downloading %s (%s)...\n", asset.Name, latest.TagName)
	tmpPath, err := DownloadReleaseBinary(*latest, *asset, "rw-core")
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}

	if err := ValidateCoreBinary(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("downloaded core failed validation: %w", err)
	}

	if targetPath == "" {
		if exe, err := os.Executable(); err == nil {
			targetPath = filepath.Join(filepath.Dir(exe), "rw-core")
		} else {
			targetPath = "./rw-core"
		}
	}

	_ = os.MkdirAll(filepath.Dir(targetPath), 0755)

	if err := AtomicReplace(tmpPath, targetPath); err != nil {
		return "", fmt.Errorf("failed to install core to %s: %w", targetPath, err)
	}

	if err := EnsureGeodata(filepath.Dir(targetPath)); err != nil {
		return "", err
	}

	fmt.Printf("[CORE] Successfully installed Xray core to %s!\n", targetPath)
	return targetPath, nil
}

func EnsureGeodata(targetDirs ...string) error {
	dir := "/usr/local/share/xray"
	if len(targetDirs) > 0 && targetDirs[0] != "" {
		dir = targetDirs[0]
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	for _, name := range []string{"geoip.dat", "geosite.dat"} {
		target := filepath.Join(dir, name)
		if fi, err := os.Stat(target); err == nil && fi.Mode().IsRegular() && fi.Size() > 1024 {
			continue
		}
		copied := false
		for _, trusted := range GetStandardAssetDirs() {
			candidate := filepath.Join(trusted, name)
			if candidate == target {
				continue
			}
			if fi, err := os.Stat(candidate); err == nil && fi.Mode().IsRegular() && fi.Size() > 1024 {
				if err := CopyFile(candidate, target); err != nil {
					return err
				}
				copied = true
				break
			}
		}
		if copied {
			continue
		}
		raw := "https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/" + name
		data, err := downloadFileBytes(raw)
		if err != nil {
			return fmt.Errorf("prepare %s: %w", name, err)
		}
		if len(data) <= 1024 {
			return fmt.Errorf("invalid geodata %s", name)
		}
		f, err := os.CreateTemp(dir, ".geodata-*")
		if err != nil {
			return err
		}
		tmp := f.Name()
		_, err = f.Write(data)
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = os.Chmod(tmp, 0644)
		}
		if err == nil {
			err = os.Rename(tmp, target)
		}
		os.Remove(tmp)
		if err != nil {
			return err
		}
	}
	return nil
}

func downloadFileBytes(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return fetchBytes(ctx, url, MaxArtifactSize)
}

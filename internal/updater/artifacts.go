package updater

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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const NodeRepository = "aaaSaZaN/remnanode-go"
const CoreRepository = "XTLS/Xray-core"
const MaxArtifactSize int64 = 128 * 1024 * 1024

// HTTPClient is shared so redirects retain HTTPS and downloads have a deadline.
var HTTPClient = &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return errors.New("artifact redirect must use HTTPS")
	}
	if len(via) > 10 {
		return errors.New("too many redirects")
	}
	return nil
}}

func fetchBytes(ctx context.Context, raw string, limit int64) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("artifact URL must use HTTPS")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Remnanode-Go")
	res, err := HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("download HTTP %d", res.StatusCode)
	}
	if res.ContentLength > limit {
		return nil, errors.New("artifact too large")
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(b) == 0 || int64(len(b)) > limit {
		return nil, errors.New("empty or oversized artifact")
	}
	return b, nil
}
func checksumFromManifest(data []byte, name string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name && len(fields[0]) == 64 {
			if _, err := hex.DecodeString(fields[0]); err == nil {
				return strings.ToLower(fields[0]), nil
			}
		}
		// Xray's per-archive .dgst includes several algorithms.
		if len(fields) == 2 && (fields[0] == "SHA2-256=" || fields[0] == "SHA256=") && len(fields[1]) == 64 {
			if _, err := hex.DecodeString(fields[1]); err == nil {
				return strings.ToLower(fields[1]), nil
			}
		}
	}
	return "", errors.New("SHA-256 entry missing for " + name)
}
func DownloadReleaseBinary(release ReleaseInfo, asset ReleaseAsset, name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	digest := strings.TrimPrefix(asset.Digest, "sha256:")
	if len(digest) != 64 {
		digest = ""
		for _, candidate := range release.Assets {
			lower := strings.ToLower(candidate.Name)
			if candidate.Name != asset.Name+".dgst" && candidate.Name != asset.Name+".sha256" && lower != "checksums.txt" && lower != "sha256sums" && lower != "sha256sums.txt" {
				continue
			}
			data, err := fetchBytes(ctx, candidate.BrowserDownloadURL, 4*1024*1024)
			if err != nil {
				return "", err
			}
			digest, err = checksumFromManifest(data, asset.Name)
			if err == nil {
				break
			}
		}
	}
	if digest == "" {
		return "", errors.New("release has no SHA-256 digest for " + asset.Name)
	}
	data, err := fetchBytes(ctx, asset.BrowserDownloadURL, MaxArtifactSize)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), digest) {
		return "", errors.New("artifact SHA-256 mismatch")
	}
	return saveBinary(data, asset.Name, name)
}
func ValidateNodeBinary(path string) error {
	out, err := binaryOutput(path, "version")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(out), "Remnawave Node (Go) v") {
		return errors.New("download is not a Remnanode Go executable")
	}
	return nil
}

// PrepareCore only stages a candidate. Activation follows successful config validation.
func PrepareCore(ctx context.Context, target string, geodata interface{}) (string, error) {
	raw, err := json.Marshal(geodata)
	if err != nil {
		return "", err
	}
	var cfg struct {
		Core *struct {
			URL    string `json:"url"`
			SHA256 string `json:"sha256"`
		} `json:"core"`
	}
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return "", err
	}
	if cfg.Core == nil {
		if _, err := os.Stat(target + ".stock"); err == nil {
			a, e1 := os.ReadFile(target)
			b, e2 := os.ReadFile(target + ".stock")
			if e1 == nil && e2 == nil && sha256.Sum256(a) == sha256.Sum256(b) {
				return target, nil
			}
			return target + ".stock", nil
		}
		return target, nil
	}
	if len(cfg.Core.SHA256) != 64 {
		return "", errors.New("custom core requires SHA-256")
	}
	if _, err = hex.DecodeString(cfg.Core.SHA256); err != nil {
		return "", errors.New("invalid custom core SHA-256")
	}
	// Reuse the active core only after hashing it, rather than trusting a marker.
	if data, err := os.ReadFile(target); err == nil {
		sum := sha256.Sum256(data)
		if strings.EqualFold(hex.EncodeToString(sum[:]), cfg.Core.SHA256) {
			return target, nil
		}
	}
	data, err := fetchBytes(ctx, cfg.Core.URL, MaxArtifactSize)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), cfg.Core.SHA256) {
		return "", errors.New("custom core SHA-256 mismatch")
	}
	path, err := saveBinary(data, "core", "rw-core")
	if err != nil {
		return "", err
	}
	if err = ValidateCoreBinary(path); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}
func PrepareAssets(ctx context.Context, dir string, geodata interface{}) error {
	raw, err := json.Marshal(geodata)
	if err != nil {
		return err
	}
	var cfg struct {
		Assets []struct {
			URL  string `json:"url"`
			File string `json:"file"`
		} `json:"assets"`
	}
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	for _, a := range cfg.Assets {
		if a.File == "" || a.File == "." || a.File == ".." || filepath.Base(a.File) != a.File || strings.ContainsAny(a.File, "/\\") {
			return errors.New("asset must be a plain file name")
		}
		target := filepath.Join(dir, a.File)
		if fi, err := os.Stat(target); err == nil && fi.Mode().IsRegular() && fi.Size() > 0 {
			continue
		}
		data, err := fetchBytes(ctx, a.URL, MaxArtifactSize)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		f, err := os.CreateTemp(dir, ".asset-*")
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
func Backup(target, suffix string) error {
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	in, err := os.Open(target)
	if err != nil {
		return err
	}
	defer in.Close()
	f, err := os.CreateTemp(filepath.Dir(target), ".backup-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = io.Copy(f, in)
	if err == nil {
		err = f.Chmod(0755)
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), target+suffix)
}
func ActivateCore(candidate, target string) error {
	if candidate == target {
		return nil
	}
	if _, err := os.Stat(target + ".stock"); errors.Is(err, os.ErrNotExist) {
		if err := Backup(target, ".stock"); err != nil {
			return err
		}
	}
	if err := Backup(target, ".previous"); err != nil {
		return err
	}
	// Copy the candidate: a managed stock artifact must survive reset.
	f, err := os.CreateTemp(filepath.Dir(target), ".core-*")
	if err != nil {
		return err
	}
	f.Close()
	defer os.Remove(f.Name())
	if err = CopyFile(candidate, f.Name()); err != nil {
		return err
	}
	return AtomicReplace(f.Name(), target)
}
func ResetCore(target string) error {
	if _, err := os.Stat(target + ".stock"); err != nil {
		return errors.New("no managed stock core is available")
	}
	return ActivateCore(target+".stock", target)
}
func HasNetAdmin() bool {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "CapEff:") {
			n, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "CapEff:")), 16, 64)
			return err == nil && n&(1<<12) != 0
		}
	}
	return false
}

func IsCustomCore(path string) bool {
	active, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	stock, err := os.ReadFile(path + ".stock")
	if err == nil {
		return sha256.Sum256(active) != sha256.Sum256(stock)
	}
	resolved, err := filepath.EvalSymlinks(path)
	return err == nil && filepath.Base(resolved) == "xray-custom"
}

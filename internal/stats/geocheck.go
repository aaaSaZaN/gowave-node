package stats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/remnawave/node-go/internal/updater"
)

// geocheckPath is where the upstream node image and older installers put
// geocheck. Without it, the pinned release is fetched for each run and removed
// afterwards; it is built for linux/amd64 and linux/arm64 only.
const geocheckPath = "/usr/local/bin/geocheck"

const geocheckReleaseURL = "https://github.com/remnawave/geocheck/releases/download/v0.3.0/"

var geocheckArchiveSHA256 = map[string]string{
	"amd64": "eca33bbee3c2c3ea7ade85f9a61ef4c7471a8334eb8f0a715e844aef865307d9",
	"arm64": "1badce683fd901b4052a78b833d536b851cf1f5952d82bc985c875e37a153267",
}

type GeocheckService struct {
	mu        sync.Mutex
	isRunning bool
}

func NewGeocheckService() *GeocheckService {
	return &GeocheckService{}
}

type GeocheckRequest struct {
	IP        *string `json:"ip"`
	Interface *string `json:"interface"`
}

func (g *GeocheckService) Run(parent context.Context, req GeocheckRequest) (map[string]interface{}, error) {
	g.mu.Lock()
	if g.isRunning {
		g.mu.Unlock()
		return nil, errors.New("Geocheck: a run is already in progress.")
	}
	g.isRunning = true
	g.mu.Unlock()

	defer func() {
		g.mu.Lock()
		g.isRunning = false
		g.mu.Unlock()
	}()

	var bindTo string
	if req.IP != nil && strings.TrimSpace(*req.IP) != "" {
		ip := strings.TrimSpace(*req.IP)
		if net.ParseIP(ip) == nil {
			return nil, fmt.Errorf("Geocheck: \"%s\" is not a valid IP address.", ip)
		}
		bindTo = ip
	} else if req.Interface != nil && strings.TrimSpace(*req.Interface) != "" {
		bindTo = strings.TrimSpace(*req.Interface)
	}

	args := []string{"--json", "--svg-base64", "--quiet"}
	if bindTo != "" {
		args = append([]string{"--interface", bindTo}, args...)
	}

	binary, cleanup, err := geocheckBinary(parent)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...)
	output := &limitedOutput{limit: 32 * 1024 * 1024}
	cmd.Stdout = output
	err = cmd.Run()
	out := output.Bytes()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, errors.New("Geocheck: execution exceeded 45s and was killed.")
		}
		return nil, fmt.Errorf("Geocheck failed: %w", err)
	}

	var report map[string]interface{}
	if err := json.Unmarshal(out, &report); err != nil {
		return nil, fmt.Errorf("Geocheck produced invalid JSON: %w", err)
	}

	image, ok := report["image"].(map[string]interface{})
	if !ok || image["data"] == nil || image["data"] == "" {
		return nil, errors.New("geocheck report carries no image")
	}
	return report, nil
}

// geocheckBinary returns the installed geocheck, or downloads the pinned
// release into a temporary directory that cleanup removes.
func geocheckBinary(parent context.Context) (string, func(), error) {
	if info, err := os.Stat(geocheckPath); err == nil && info.Mode().IsRegular() {
		return geocheckPath, func() {}, nil
	}
	sum, ok := geocheckArchiveSHA256[runtime.GOARCH]
	if runtime.GOOS != "linux" || !ok {
		return "", nil, fmt.Errorf("Geocheck: no build for %s/%s.", runtime.GOOS, runtime.GOARCH)
	}
	dir, err := geocheckTempDir()
	if err != nil {
		return "", nil, fmt.Errorf("Geocheck: %w", err)
	}
	cleanup := func() { os.RemoveAll(dir) }

	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	binary := filepath.Join(dir, "geocheck")
	archive := geocheckReleaseURL + "geocheck_linux_" + runtime.GOARCH + ".tar.gz"
	if err := updater.FetchPinnedTarGzExecutable(ctx, archive, sum, "geocheck", binary); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("Geocheck: download failed: %w", err)
	}
	return binary, cleanup, nil
}

// geocheckTempDir prefers the node's own directory because /tmp may be
// mounted noexec.
func geocheckTempDir() (string, error) {
	if exe, err := os.Executable(); err == nil {
		if dir, err := os.MkdirTemp(filepath.Dir(exe), ".geocheck-"); err == nil {
			return dir, nil
		}
	}
	return os.MkdirTemp("", "geocheck-")
}

type limitedOutput struct {
	bytes.Buffer
	limit int
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	if len(p) > b.limit-b.Len() {
		return 0, errors.New("geocheck output exceeds 32 MiB")
	}
	return b.Buffer.Write(p)
}

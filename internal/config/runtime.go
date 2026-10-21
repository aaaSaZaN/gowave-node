package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func RuntimeDir() string {
	if dir := os.Getenv("GOWAVE_NODE_RUNTIME_DIR"); dir != "" {
		return dir
	}
	return "/run/gowave-node"
}

// PrivateDir refuses symlinks and directories belonging to another user.
func PrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe runtime directory %s", dir)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("runtime directory is not owned by this user")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return err
	}
	return nil
}

func WritePrivate(path string, data []byte) error {
	if err := PrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".stage-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// SocketAddress accepts the original abstract names, absolute Unix paths, and
// explicit loopback TCP endpoints. It never silently changes transports.
func SocketAddress(endpoint string) (network, address string, err error) {
	if strings.HasPrefix(endpoint, "unix:") {
		endpoint = strings.TrimPrefix(endpoint, "unix:")
	}
	if filepath.IsAbs(endpoint) {
		return "unix", endpoint, nil
	}
	if strings.HasPrefix(endpoint, "@") {
		return "unix", "\x00" + strings.TrimPrefix(endpoint, "@"), nil
	}
	if !strings.Contains(endpoint, ":") && endpoint != "" {
		return "unix", "\x00" + endpoint, nil
	}
	host, port, e := net.SplitHostPort(endpoint)
	if e != nil || port == "" {
		return "", "", fmt.Errorf("invalid management endpoint %q", endpoint)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", "", fmt.Errorf("management TCP endpoint must use a loopback IP")
	}
	return "tcp", endpoint, nil
}

type Discovery struct {
	PID    int    `json:"pid"`
	Socket string `json:"socket"`
	Token  string `json:"token"`
}

func SaveDiscovery(cfg *Config) error {
	b, err := json.Marshal(Discovery{Socket: cfg.InternalSocketPath, Token: cfg.InternalRestToken, PID: os.Getpid()})
	if err != nil {
		return err
	}
	return WritePrivate(filepath.Join(RuntimeDir(), "connection.json"), b)
}

func ReadDiscovery() (Discovery, error) {
	var d Discovery
	b, err := os.ReadFile(filepath.Join(RuntimeDir(), "connection.json"))
	if err != nil {
		return d, err
	}
	err = json.Unmarshal(b, &d)
	return d, err
}

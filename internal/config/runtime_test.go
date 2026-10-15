package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagementEndpoints(t *testing.T) {
	tests := []struct {
		input, network, address string
		fail                    bool
	}{
		{"/run/node/api.sock", "unix", "/run/node/api.sock", false}, {"@api", "unix", "\x00api", false}, {"legacy-api", "unix", "\x00legacy-api", false}, {"127.0.0.1:62085", "tcp", "127.0.0.1:62085", false}, {"[::1]:62085", "tcp", "[::1]:62085", false}, {"0.0.0.0:62085", "", "", true}, {"example.com:80", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			network, address, err := SocketAddress(tt.input)
			if (err != nil) != tt.fail || network != tt.network || address != tt.address {
				t.Fatalf("%q %q %v", network, address, err)
			}
		})
	}
}
func TestPrivatePublicationAndSymlinkRefusal(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "private")
	path := filepath.Join(dir, "secret")
	if err := WritePrivate(path, []byte("secret")); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{dir: 0700, path: 0600} {
		info, err := os.Stat(name)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("%s: %v %v", name, info, err)
		}
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivate(filepath.Join(link, "other"), []byte("x")); err == nil {
		t.Fatal("accepted symlink runtime directory")
	}
}
func TestRandomTokensDiffer(t *testing.T) {
	a, err := RandomToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := RandomToken()
	if err != nil || a == b || len(a) != 64 {
		t.Fatal("invalid random token")
	}
}

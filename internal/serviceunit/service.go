// Package serviceunit installs the native service's privilege boundary.
package serviceunit

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

//go:embed install.sh
var installer string

func Install(dir, unitDir string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command("bash", "-s", "--", dir, unitDir, exe)
	cmd.Stdin = strings.NewReader(installer)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// MigrateLegacy runs only inside an existing root systemd service, before any
// application work. After daemon-reload, Restart=always starts it as its user.
// This also covers updates performed by older binaries and installers.
func MigrateLegacy() (bool, error) {
	if os.Geteuid() != 0 || os.Getenv("INVOCATION_ID") == "" {
		return false, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return true, err
	}
	if dir == "/" {
		dir = "/opt/remnanode"
	}
	if err := Install(dir, "/etc/systemd/system"); err != nil {
		return true, fmt.Errorf("migrate unprivileged service: %w", err)
	}
	return true, nil
}

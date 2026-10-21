package xray

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const stockS6Body = `exec 2>&1
exec /usr/local/bin/rw-core \
    -config @"${INTERNAL_SOCKET_PATH}":/internal/get-config?token="${INTERNAL_REST_TOKEN}" \
    -format json`
const managedS6Marker = "# managed by gowave-node: private file configuration\n"

func recognizedStockRun(data []byte) bool {
	text := strings.TrimSpace(string(data))
	line, body, ok := strings.Cut(text, "\n")
	return ok && (line == "#!/command/with-contenv sh" || line == "#!/bin/sh") && strings.TrimSpace(body) == stockS6Body
}
func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
func writeRun(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".run-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(data)
	if err == nil {
		err = f.Chmod(mode)
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// The stock script's Unix-HTTP loader is absent from some stable Xray releases.
// Only the exact reference script (or our own managed version) is adapted. The
// original is restored when the supervised process stops, including on shutdown.
func (p *ProcessManager) prepareS6Run() error {
	path := filepath.Join(p.serviceDir, "run")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	backup := path + ".gowave-node-original"
	managed := strings.Contains(string(data), managedS6Marker)
	if !recognizedStockRun(data) && !managed {
		return nil
	}
	if !managed {
		f, err := os.OpenFile(backup, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			_, err = f.Write(data)
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
			if err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	original, err := os.ReadFile(backup)
	if err != nil {
		return err
	}
	if !recognizedStockRun(original) {
		return errors.New("unrecognized s6 backup; refusing to replace run script")
	}
	header, _, _ := strings.Cut(string(original), "\n")
	script := header + "\n" + managedS6Marker + "exec 2>&1\nexport XRAY_LOCATION_ASSET=" + shellQuote(p.assetDir) + "\nexec " + shellQuote(p.execPath) + " run -format json -config " + shellQuote(p.configPath) + "\n"
	return writeRun(path, []byte(script), 0755)
}
func (p *ProcessManager) restoreS6Run() error {
	path := filepath.Join(p.serviceDir, "run")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), managedS6Marker) {
		return nil
	}
	original, err := os.ReadFile(path + ".gowave-node-original")
	if err != nil {
		return err
	}
	if !recognizedStockRun(original) {
		return errors.New("invalid s6 backup")
	}
	return writeRun(path, original, 0755)
}

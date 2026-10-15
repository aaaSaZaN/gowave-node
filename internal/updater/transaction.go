package updater

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func LockTarget(target string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".update.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another update is in progress: %w", err)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

// InstallStockCore updates both the active core and the default selected by a
// later panel start with no custom-core configuration.
func InstallStockCore(candidate, target string) error {
	if err := Backup(target, ".previous"); err != nil {
		return err
	}
	marker := "remove"
	if _, err := os.Stat(target + ".stock"); err == nil {
		marker = "restore"
		if err := Backup(target+".stock", ".previous"); err != nil {
			return err
		}
	}
	if err := os.WriteFile(target+".stock.pending", []byte(marker), 0600); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".stock-*")
	if err != nil {
		return err
	}
	f.Close()
	defer os.Remove(f.Name())
	if err := CopyFile(candidate, f.Name()); err != nil {
		return err
	}
	if err := AtomicReplace(candidate, target); err != nil {
		return err
	}
	if err := AtomicReplace(f.Name(), target+".stock"); err != nil {
		_ = Rollback(target)
		return err
	}
	return nil
}
func CommitUpdate(target string) { _ = os.Remove(target + ".stock.pending") }

package controlplane

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wudi000888-svg/guangyue-panel/backend/internal/persistence"
)

func lockControllerDirectory(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "controller.lock"), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("site state directory already has a controller; stop it before maintenance")
	}
	return f, nil
}
func offlineSnapshot(cfg Config, s *Store, path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(path)
		}
	}()
	dir, err := os.MkdirTemp(cfg.StateDir, "snapshot-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	a := &App{cfg: cfg, store: s}
	if err = a.backupSnapshot(dir); err != nil {
		return err
	}
	if err = writeBackup(f, dir); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	ok = true
	return nil
}
func importSQLiteSite(cfg Config, dst *Store, path string) error {
	if !cfg.controller() {
		return errors.New("SQLite migration requires Pro edition")
	}
	if filepath.Base(path) != "panel.db" {
		return errors.New("source database must be named panel.db")
	}
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return errors.New("source database is missing")
	}
	sourceDir := filepath.Dir(path)
	srcKey, err := os.ReadFile(filepath.Join(sourceDir, "master.key"))
	if err != nil {
		return err
	}
	dstKey, err := os.ReadFile(filepath.Join(cfg.StateDir, "master.key"))
	if err != nil {
		return err
	}
	if !bytes.Equal(srcKey, dstKey) {
		return errors.New("copy the source master.key into the empty destination state directory before migration")
	}
	src, err := openStore(sourceDir)
	if err != nil {
		return err
	}
	defer src.db.Close()
	if err = src.validateCommerce(true); err != nil {
		return err
	}
	if _, err = src.records(); err != nil {
		return errors.New("source credentials cannot be decrypted")
	}
	if _, err = src.nodes(); err != nil {
		return errors.New("source nodes cannot be decrypted")
	}
	if _, err = src.pools(); err != nil {
		return errors.New("source exits cannot be decrypted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err = persistence.Copy(ctx, src.db, dst.db, true); err != nil {
		return err
	}
	return dst.initRuntimeSettings()
}

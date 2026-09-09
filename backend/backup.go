package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const maxBackupDatabaseBytes = 64 << 20

var backupEntries = []string{"config.json", "master.key", "panel.db"}

func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Hold the control-plane lock only while capturing the database and its key.
// Hashing, compression and slow-client writes use files and bounded buffers.
func (a *App) backupSnapshot(dir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.store.db.Exec("VACUUM INTO ?", filepath.Join(dir, "panel.db")); err != nil {
		return err
	}
	info, err := os.Stat(filepath.Join(dir, "panel.db"))
	if err != nil {
		return err
	}
	if info.Size() > maxBackupDatabaseBytes {
		return errors.New("database exceeds restore size limit")
	}
	if err := os.Chmod(filepath.Join(dir, "panel.db"), 0600); err != nil {
		return err
	}
	key, err := os.ReadFile(filepath.Join(a.cfg.StateDir, "master.key"))
	if err != nil {
		return err
	}
	if err = atomicWrite(filepath.Join(dir, "master.key"), key, 0600); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, "config.json"), a.cfg)
}

func writeBackup(dst io.Writer, dir string) (err error) {
	manifest := map[string]string{}
	for _, name := range backupEntries {
		manifest[name], err = fileDigest(filepath.Join(dir, name))
		if err != nil {
			return err
		}
	}
	if err = writeJSON(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		return err
	}
	gz := gzip.NewWriter(dst)
	tw := tar.NewWriter(gz)
	defer func() { err = errors.Join(err, tw.Close(), gz.Close()) }()
	for _, name := range append([]string{"manifest.json"}, backupEntries...) {
		if err = appendBackupEntry(tw, filepath.Join(dir, name), name); err != nil {
			return err
		}
	}
	return nil
}
func appendBackupEntry(tw *tar.Writer, path, name string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if err = tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: info.Size(), ModTime: info.ModTime()}); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}
func (a *App) backup(w http.ResponseWriter, r *http.Request, actor Record) {
	if !a.heavyMu.TryLock() {
		failure(w, 409, "后台任务繁忙，请稍后重试")
		return
	}
	defer a.heavyMu.Unlock()
	dir, err := os.MkdirTemp(a.cfg.StateDir, "backup-")
	if err != nil {
		failure(w, 500, "备份目录创建失败")
		return
	}
	defer os.RemoveAll(dir)
	if err = a.backupSnapshot(dir); err != nil {
		failure(w, 500, "数据库快照失败")
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", "attachment; filename=guangyue-"+time.Now().UTC().Format("20060102-150405")+".tar.gz")
	if err = writeBackup(w, dir); err == nil {
		a.store.audit(actor.Username, "download_backup", "encrypted database and recovery key")
	}
}

func extractBackup(path, dir string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(io.LimitReader(file, 128<<20))
	if err != nil {
		return err
	}
	defer gz.Close()
	gz.Multistream(false)
	tr := tar.NewReader(gz)
	limits := map[string]int64{"manifest.json": 4096, "config.json": 1 << 20, "master.key": 32, "panel.db": maxBackupDatabaseBytes}
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		limit, allowed := limits[h.Name]
		if !allowed || seen[h.Name] || h.Typeflag != tar.TypeReg || h.Size < 0 || h.Size > limit {
			return errors.New("invalid backup entry")
		}
		seen[h.Name] = true
		f, err := os.OpenFile(filepath.Join(dir, h.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(f, tr, h.Size)
		if err = errors.Join(copyErr, f.Close()); err != nil {
			return err
		}
	}
	// Consume the gzip trailer too: tar EOF alone does not verify its checksum.
	n, err := io.Copy(io.Discard, io.LimitReader(gz, 1<<20))
	if err != nil {
		return err
	}
	if n == 1<<20 || len(seen) != len(limits) {
		return errors.New("incomplete or oversized backup")
	}
	var manifest map[string]string
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &manifest); err != nil {
		return err
	}
	for _, name := range backupEntries {
		sum, err := fileDigest(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if sum != manifest[name] {
			return errors.New("backup checksum mismatch")
		}
	}
	key, err := os.Stat(filepath.Join(dir, "master.key"))
	if err != nil {
		return err
	}
	if key.Size() != 32 {
		return errors.New("invalid recovery key")
	}
	return nil
}

// Validate every encrypted collection, migrations and generated configurations
// in staging. No live files move until this entire operation succeeds.
func prepareRestore(cfg Config, dir string) (err error) {
	check, err := openStore(dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, check.db.Close()) }()
	var integrity string
	if err = check.db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return errors.New("database integrity check failed")
	}
	if _, err = check.records(); err != nil {
		return err
	}
	nodes, err := check.nodes()
	if err != nil {
		return err
	}
	for _, node := range nodes {
		if err = validateNode(node); err != nil {
			return err
		}
		if err = validateNodeDNS(node.DNS); err != nil {
			return err
		}
	}
	if _, err = check.pools(); err != nil {
		return err
	}
	if _, err = check.importSources(); err != nil {
		return err
	}
	if _, err = check.publicSourceCatalog(); err != nil {
		return err
	}
	if err = check.migratePublicSubscriptions(); err != nil {
		return err
	}
	if err = check.migratePools(); err != nil {
		return err
	}
	if err = check.ensureDefaultDirectNodes(); err != nil {
		return err
	}
	if _, err = check.db.Exec("DELETE FROM sessions; DELETE FROM checkpoints;"); err != nil {
		return err
	}
	cfg.StateDir = dir
	app := &App{cfg: cfg, store: check}
	if err = app.prepare(); err != nil {
		return err
	}
	if err = check.markPreparedNodes(); err != nil {
		return err
	}
	var busy, pages, checkpointed int
	if err = check.db.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &pages, &checkpointed); err != nil {
		return err
	}
	if busy != 0 {
		return errors.New("restore checkpoint is busy")
	}
	return nil
}

// All renames are on the same filesystem. On a reported install failure undo
// both phases, retaining the previous directory if rollback itself fails.
func installRestore(dir, staged, previous string, rename func(string, string) error) (err error) {
	names := []string{"panel.db", "panel.db-wal", "panel.db-shm", "master.key", "xray.json", "hy2.json", runtimeModeFile, runtimeCommitFile}
	moved, installed := []string{}, []string{}
	defer func() {
		if err == nil {
			return
		}
		for i := len(installed) - 1; i >= 0; i-- {
			err = errors.Join(err, os.Remove(filepath.Join(dir, installed[i])))
		}
		for i := len(moved) - 1; i >= 0; i-- {
			err = errors.Join(err, rename(filepath.Join(previous, moved[i]), filepath.Join(dir, moved[i])))
		}
	}()
	for _, name := range names {
		if _, statErr := os.Lstat(filepath.Join(dir, name)); statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			return statErr
		}
		if err = rename(filepath.Join(dir, name), filepath.Join(previous, name)); err != nil {
			return err
		}
		moved = append(moved, name)
	}
	for _, name := range names {
		if name == "panel.db-wal" || name == "panel.db-shm" {
			continue
		}
		if err = rename(filepath.Join(staged, name), filepath.Join(dir, name)); err != nil {
			return err
		}
		installed = append(installed, name)
	}
	return nil
}
func restoreBackup(cfg Config, path string) error {
	if corePID("guangyue.service") > 0 || corePID("guangyue-xray.service") > 0 || corePID("guangyue-hy2.service") > 0 {
		return errors.New("stop guangyue, guangyue-xray and guangyue-hy2 before restoring")
	}
	temp, err := os.MkdirTemp(cfg.StateDir, "restore-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	if err = extractBackup(path, temp); err != nil {
		return err
	}
	var original Config
	b, err := os.ReadFile(filepath.Join(temp, "config.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &original); err != nil {
		return err
	}
	if original.RealityPublic != cfg.RealityPublic || original.PublicURL != cfg.PublicURL {
		return errors.New("restore the matching configuration file first")
	}
	if err = prepareRestore(cfg, temp); err != nil {
		return err
	}
	previous, err := os.MkdirTemp(cfg.StateDir, "before-restore-")
	if err != nil {
		return err
	}
	if err = installRestore(cfg.StateDir, temp, previous, os.Rename); err != nil {
		return fmt.Errorf("restore failed; previous files at %s: %w", previous, err)
	}
	return nil
}

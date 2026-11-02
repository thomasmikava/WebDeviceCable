package relay

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

func maintenanceDB(directory string, writable bool) (*sql.DB, error) {
	path, err := filepath.Abs(filepath.Join(directory, "relay.sqlite"))
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("existing regular relay database required")
	}
	uriPath := filepath.ToSlash(path)
	// A Windows drive belongs in the URI path, never in its authority.
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	mode := "ro"
	if writable {
		mode = "rw"
	}
	u.RawQuery = "mode=" + mode
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA busy_timeout=10000"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Backup creates a consistent SQLite snapshot, including committed WAL records.
// Only completed WDC backup files count toward retention; unrelated files remain untouched.
func Backup(directory, destination string, retain int) (string, error) {
	if retain < 1 {
		return "", errors.New("backup retention must be positive")
	}
	db, err := maintenanceDB(directory, false)
	if err != nil {
		return "", err
	}
	defer db.Close()
	if err = os.MkdirAll(destination, 0700); err != nil {
		return "", err
	}
	if err = os.Chmod(destination, 0700); err != nil {
		return "", err
	}
	lock := flock.New(filepath.Join(destination, "backup.lock"))
	if err = lock.Lock(); err != nil {
		return "", err
	}
	defer lock.Close()
	file, err := os.CreateTemp(destination, "wdc-"+time.Now().UTC().Format("20060102T150405.000000000Z")+"-*.sqlite.partial")
	if err != nil {
		return "", err
	}
	temporary := file.Name()
	path := strings.TrimSuffix(temporary, ".partial")
	defer func() {
		file.Close()
		os.Remove(temporary)
	}()
	if _, err = db.Exec("VACUUM main INTO ?", temporary); err != nil {
		return "", err
	}
	if _, err = db.Exec("ATTACH DATABASE ? AS snapshot", temporary); err != nil {
		return "", err
	}
	var integrity string
	err = db.QueryRow("PRAGMA snapshot.quick_check").Scan(&integrity)
	_, _ = db.Exec("DETACH DATABASE snapshot")
	if err != nil || integrity != "ok" {
		return "", errors.New("backup integrity check failed")
	}
	if err = file.Sync(); err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(temporary, path); err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" {
		dir, err := os.Open(destination)
		if err != nil {
			return "", err
		}
		err = dir.Sync()
		dir.Close()
		if err != nil {
			return "", err
		}
	}
	files, err := filepath.Glob(filepath.Join(destination, "wdc-*.sqlite"))
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	for len(files) > retain {
		// Always preserve this completed snapshot even if the host clock moved backwards.
		if files[0] == path {
			files = append(files[1:], path)
		}
		if err := os.Remove(files[0]); err != nil {
			return path, err
		}
		files = files[1:]
	}
	return path, nil
}

// FenceRestored requires the server's exclusive directory lock. It saves a
// forensic backup, revokes restored connections/codes, and preserves projects/receipts.
func FenceRestored(directory, backups string) error {
	lock := flock.New(filepath.Join(directory, "relay.lock"))
	ok, err := lock.TryLock()
	if err != nil || !ok {
		return errors.New("stop the relay before fencing restored state")
	}
	defer lock.Close()
	if _, err = Backup(directory, backups, 7); err != nil {
		return err
	}
	db, err := maintenanceDB(directory, true)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query("SELECT id,data FROM records WHERE kind='connection'")
	if err != nil {
		return err
	}
	var connections []connection
	for rows.Next() {
		var id string
		var raw []byte
		var c connection
		if err = rows.Scan(&id, &raw); err != nil {
			break
		}
		if err = json.Unmarshal(raw, &c); err != nil || c.ID != id || c.ProjectID == "" {
			err = errors.New("invalid restored connection ownership")
			break
		}
		connections = append(connections, c)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, c := range connections {
		c.Revoked, c.PrivateKey, c.Invitation, c.TokenHash, c.Identity.Secret = true, "", "", "", ""
		raw, err := json.Marshal(c)
		if err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE records SET data=? WHERE kind='connection' AND id=?", string(raw), c.ID); err != nil {
			return fmt.Errorf("fence connection: %w", err)
		}
	}
	if _, err = tx.Exec("DELETE FROM records WHERE kind='ticket'"); err != nil {
		return err
	}
	return tx.Commit()
}

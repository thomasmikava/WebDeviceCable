package relay

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBackupIncludesCommittedWALAndRetainsOnlyOwnSnapshots(t *testing.T) {
	s, err := Open(Config{Directory: t.TempDir(), AdminKey: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.put("facade", "consumed", "keep-receipt"); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("UPDATE records SET data='uncommitted' WHERE id='consumed'"); err != nil {
		t.Fatal(err)
	}
	backups := t.TempDir()
	other := filepath.Join(backups, "unrelated.sqlite")
	if err = os.WriteFile(other, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		path, err := Backup(s.config.Directory, backups, 2)
		if err != nil {
			t.Fatal(err)
		}
		// Open the snapshot independently while the source writer is still uncommitted.
		dir := t.TempDir()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "relay.sqlite"), data, 0600); err != nil {
			t.Fatal(err)
		}
		db, err := maintenanceDB(dir, false)
		if err != nil {
			t.Fatal(err)
		}
		var value string
		err = db.QueryRow("SELECT data FROM records WHERE id='consumed'").Scan(&value)
		db.Close()
		if err != nil || value != `"keep-receipt"` {
			t.Fatalf("snapshot lost committed receipt: %q %v", value, err)
		}
		info, _ := os.Stat(path)
		if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
			t.Fatal("snapshot is accessible to other users")
		}
	}
	files, _ := filepath.Glob(filepath.Join(backups, "wdc-*.sqlite"))
	if len(files) != 2 {
		t.Fatalf("retained %d snapshots", len(files))
	}
	if data, err := os.ReadFile(other); err != nil || string(data) != "keep" {
		t.Fatal("unrelated file changed")
	}
}

func TestFenceRestoredRequiresOfflineServerAndPreservesLedger(t *testing.T) {
	cfg := Config{Directory: t.TempDir(), AdminKey: strings.Repeat("a", 64)}
	s, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			s.Close()
		}
	}()
	p := (registryHarness{t, s}).create(cfg.AdminKey, "recovery-project")
	c := connection{ID: "connection", ProjectID: p.Project.ID, PrivateKey: "private-fixture", TokenHash: "token-fixture", Invitation: "invitation-fixture", Identity: invitation{AppID: p.Project.AppID, Origin: p.Project.Origin, Secret: "pairing-fixture"}}
	for kind, value := range map[string]any{"connection": c, "ticket": ticket{ProjectID: p.Project.ID}, "facade": "consumed-hash"} {
		if err = s.put(kind, "connection", value); err != nil {
			t.Fatal(err)
		}
	}
	backups := t.TempDir()
	if err = FenceRestored(cfg.Directory, backups); err == nil {
		t.Fatal("live relay was fenced")
	}
	s.Close()
	if err = FenceRestored(cfg.Directory, backups); err != nil {
		t.Fatal(err)
	}
	s, err = Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var restored connection
	if err = s.get("connection", c.ID, &restored); err != nil || !restored.Revoked || restored.PrivateKey != "" || restored.TokenHash != "" || restored.Invitation != "" || restored.Identity.Secret != "" || restored.ProjectID != c.ProjectID {
		t.Fatalf("restored connection was not fenced: %v", err)
	}
	var receipt string
	if err = s.get("facade", "connection", &receipt); err != nil || receipt != "consumed-hash" {
		t.Fatal("consumed receipt changed")
	}
	var count int
	if err = s.db.QueryRow("SELECT count(*) FROM records WHERE kind='ticket'").Scan(&count); err != nil || count != 0 || s.projects[p.Project.ID] == nil {
		t.Fatal("tickets or project records incorrect")
	}
	files, _ := filepath.Glob(filepath.Join(backups, "wdc-*.sqlite"))
	if len(files) != 1 {
		t.Fatal("forensic snapshot missing")
	}
}

func TestMaintenanceRefusesMissingStateAndCorruptOwnership(t *testing.T) {
	dir, backups := t.TempDir(), t.TempDir()
	if _, err := Backup(dir, backups, 1); err == nil {
		t.Fatal("missing state accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "relay.sqlite")); !os.IsNotExist(err) {
		t.Fatal("maintenance created an empty database")
	}
	s, err := Open(Config{Directory: dir, AdminKey: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO records VALUES('connection','wrong','{"ID":"another","ProjectID":"project"}',0)`); err != nil {
		t.Fatal(err)
	}
	if err = s.put("ticket", "keep-on-failure", "fixture"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err = FenceRestored(dir, backups); err == nil {
		t.Fatal("invalid ownership accepted")
	}
	db, err := maintenanceDB(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err = db.QueryRow("SELECT count(*) FROM records WHERE kind='ticket'").Scan(&count); err != nil || count != 1 {
		t.Fatal("failed fencing modified state")
	}
}

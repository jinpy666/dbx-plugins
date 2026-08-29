package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenAt(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatalf("OpenAt() error = %v", err)
	}
	return st
}

func TestOpenCreatesDir(t *testing.T) {
	base := t.TempDir()
	st, err := OpenAt(filepath.Join(base, "nested", "data"))
	if err != nil {
		t.Fatalf("OpenAt() error = %v", err)
	}
	info, err := os.Stat(st.Dir())
	if err != nil || !info.IsDir() {
		t.Fatalf("data dir not created: %v", err)
	}
}

func TestOpenFallsBackToTempDir(t *testing.T) {
	t.Setenv(EnvDataDir, "")
	st, err := Open()
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if st.Dir() == "" || filepath.Base(st.Dir()) != DefaultDirName {
		t.Errorf("fallback dir = %q", st.Dir())
	}
}

func TestOpenEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvDataDir, dir)
	st, err := Open()
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if st.Dir() != dir {
		t.Errorf("dir = %q, want %q", st.Dir(), dir)
	}
}

func TestPrefsRoundTrip(t *testing.T) {
	st := openTestStore(t)

	// 不存在 → 空 map，无错误。
	prefs, err := st.LoadPrefs()
	if err != nil {
		t.Fatalf("LoadPrefs() error = %v", err)
	}
	if len(prefs) != 0 {
		t.Fatalf("LoadPrefs() = %v, want empty", prefs)
	}

	prefs["tree.sort"] = "dn"
	prefs["panel.schema"] = true
	prefs["page.size"] = float64(50)
	if err := st.SavePrefs(prefs); err != nil {
		t.Fatalf("SavePrefs() error = %v", err)
	}

	loaded, err := st.LoadPrefs()
	if err != nil {
		t.Fatalf("LoadPrefs() error = %v", err)
	}
	if loaded["tree.sort"] != "dn" || loaded["panel.schema"] != true || loaded["page.size"] != float64(50) {
		t.Errorf("LoadPrefs() round-trip mismatch: %v", loaded)
	}

	// SavePrefs(nil) 写空对象而非报错。
	if err := st.SavePrefs(nil); err != nil {
		t.Fatalf("SavePrefs(nil) error = %v", err)
	}
}

func TestSaveJSONIsAtomicAndPrivate(t *testing.T) {
	st := openTestStore(t)
	if err := st.SaveJSON("presets.json", map[string]any{"v": 1}); err != nil {
		t.Fatalf("SaveJSON() error = %v", err)
	}
	info, err := os.Stat(filepath.Join(st.Dir(), "presets.json"))
	if err != nil {
		t.Fatalf("stat presets.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("presets.json perm = %v, want 0600", perm)
	}
	// 无临时文件残留。
	if _, err := os.Stat(filepath.Join(st.Dir(), "presets.json.tmp")); !os.IsNotExist(err) {
		t.Errorf("tmp file leftover: %v", err)
	}
}

func TestAppendAuditFormat(t *testing.T) {
	st := openTestStore(t)

	rec := AuditRecord{
		ConnectionID: "conn-1",
		Action:       "ldap/entry/delete",
		Target:       "cn=a,dc=example,dc=com",
		Result:       "ok",
	}
	if err := st.AppendAudit(rec); err != nil {
		t.Fatalf("AppendAudit() error = %v", err)
	}
	// 第二条：Time/Result 留空走兜底。
	if err := st.AppendAudit(AuditRecord{ConnectionID: "conn-1", Action: "ldap/entry/add", Target: "cn=b", Result: "denied"}); err != nil {
		t.Fatalf("AppendAudit() #2 error = %v", err)
	}

	lines, err := st.ReadAuditLines()
	if err != nil {
		t.Fatalf("ReadAuditLines() error = %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("audit lines = %d, want 2", len(lines))
	}
	if _, err := time.Parse(time.RFC3339, lines[0].Time); err != nil {
		t.Errorf("audit time not RFC3339: %q (%v)", lines[0].Time, err)
	}
	if lines[0].Action != "ldap/entry/delete" || lines[0].Target != "cn=a,dc=example,dc=com" || lines[0].Result != "ok" {
		t.Errorf("line0 = %+v", lines[0])
	}
	if lines[1].Result != "denied" {
		t.Errorf("line1 result = %q, want denied", lines[1].Result)
	}
}

func TestReadAuditLinesEmpty(t *testing.T) {
	st := openTestStore(t)
	lines, err := st.ReadAuditLines()
	if err != nil {
		t.Fatalf("ReadAuditLines() error = %v", err)
	}
	if lines != nil && len(lines) != 0 {
		t.Errorf("ReadAuditLines() = %v, want nil/empty", lines)
	}
}

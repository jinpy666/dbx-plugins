package store

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestOpenFallbackIsPersistentDir(t *testing.T) {
	// 接线测试（非平台分支）：清掉两个环境变量，按当前 GOOS 把对应平台根
	// 指到临时目录，避免在真实 HOME/APPDATA 下创建目录。
	t.Setenv(EnvDataDir, "")
	t.Setenv(EnvDataRoot, "")
	switch runtime.GOOS {
	case "darwin":
		t.Setenv("HOME", t.TempDir())
	case "windows":
		t.Setenv("APPDATA", t.TempDir())
	default:
		t.Setenv("XDG_DATA_HOME", t.TempDir())
	}
	st, err := Open()
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if st.Dir() == "" || filepath.Base(st.Dir()) != DefaultDirName {
		t.Errorf("fallback dir = %q", st.Dir())
	}
	// 修复点：无环境变量时不得再落系统临时目录（macOS $TMPDIR 重启清空）。
	if strings.HasPrefix(st.Dir(), os.TempDir()+string(filepath.Separator)) {
		t.Errorf("fallback dir %q still under TempDir", st.Dir())
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

// TestResolveDataDir 覆盖统一解析顺序（fake getenv + 显式 goos，不依赖真实环境）。
func TestResolveDataDir(t *testing.T) {
	home := "/home/u"
	appdata := `C:\Users\u\AppData\Roaming`
	xdg := "/home/u/.xdg-data"
	explicit := "/explicit/plugin-data"
	dbxData := "/host-data"

	tests := []struct {
		name   string
		getenv map[string]string
		goos   string
		want   string
	}{
		{
			// ① DBX_PLUGIN_DATA_DIR 优先，压过其他所有变量。
			name:   "plugin data dir wins",
			getenv: map[string]string{EnvDataDir: explicit, EnvDataRoot: dbxData, "HOME": home},
			goos:   "darwin",
			want:   explicit,
		},
		{
			// ② 空白字符串视为未设，落到下一级。
			name:   "blank value treated as unset",
			getenv: map[string]string{EnvDataDir: "   ", EnvDataRoot: dbxData, "HOME": home},
			goos:   "darwin",
			want:   filepath.Join(dbxData, "plugin-data", DefaultDirName),
		},
		{
			// ③ DBX_DATA_DIR → <root>/plugin-data/io.dbx.ldap。
			name:   "dbx data dir joins plugin-data",
			getenv: map[string]string{EnvDataRoot: dbxData, "HOME": home},
			goos:   "darwin",
			want:   filepath.Join(dbxData, "plugin-data", DefaultDirName),
		},
		{
			// ④ darwin：$HOME/Library/Application Support/dbx-plugin-data/<id>。
			name:   "darwin home",
			getenv: map[string]string{"HOME": home},
			goos:   "darwin",
			want:   filepath.Join(home, "Library", "Application Support", "dbx-plugin-data", DefaultDirName),
		},
		{
			// ⑤ linux：XDG_DATA_HOME 优先于 ~/.local/share。
			name:   "linux xdg set",
			getenv: map[string]string{"XDG_DATA_HOME": xdg, "HOME": home},
			goos:   "linux",
			want:   filepath.Join(xdg, "dbx-plugin-data", DefaultDirName),
		},
		{
			name:   "linux xdg unset",
			getenv: map[string]string{"HOME": home},
			goos:   "linux",
			want:   filepath.Join(home, ".local", "share", "dbx-plugin-data", DefaultDirName),
		},
		{
			// ⑥ windows：%APPDATA%\dbx-plugin-data\<id>。
			name:   "windows appdata",
			getenv: map[string]string{"APPDATA": appdata},
			goos:   "windows",
			want:   filepath.Join(appdata, "dbx-plugin-data", DefaultDirName),
		},
		{
			// ⑦ 全缺（HOME 未设等）→ 最后兜底 TempDir，永不失败。
			name:   "all missing falls back to tempdir",
			getenv: map[string]string{},
			goos:   "darwin",
			want:   filepath.Join(os.TempDir(), "dbx-plugin-data", DefaultDirName),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string { return tt.getenv[key] }
			if got := ResolveDataDir(getenv, tt.goos); got != tt.want {
				t.Errorf("ResolveDataDir() = %q, want %q", got, tt.want)
			}
		})
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

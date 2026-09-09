// Package store 管理插件数据目录（store.ResolveDataDir 统一解析）下的
// 本地数据与审计（shared/IMPL_PLAN_M0_COMMON.zh-CN.md §4）。
//
//	<解析出的数据目录>/              优先级见 ResolveDataDir 注释
//	├── prefs.json                   UI 偏好（非敏感）
//	├── presets.json                 LDAP 搜索预设（明文，不含凭据）
//	└── audit.jsonl                  写操作审计（append-only）
//
// 缺省 fallback 已改为平台持久用户数据目录（宿主从未注入
// DBX_PLUGIN_DATA_DIR，旧 $TMPDIR 兜底在 macOS 重启时清空导致丢数据）。
//
// 凭据红线：任何文件不落密码/私钥/token；审计记录 target 只记 DN，不记值。
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DefaultDirName 是数据目录路径末段的插件目录名。
const DefaultDirName = "io.dbx.ldap"

// EnvDataDir 是宿主注入的数据目录环境变量。
const EnvDataDir = "DBX_PLUGIN_DATA_DIR"

// EnvDataRoot 是宿主便携/web 模式的数据根目录环境变量（子进程继承）。
const EnvDataRoot = "DBX_DATA_DIR"

// Store 绑定一个数据目录。
type Store struct {
	dir string
}

// ResolveDataDir 按四插件统一顺序解析插件数据目录（取第一个可用项，
// "可用" = 环境变量存在且 TrimSpace 后非空）：
//
//  1. DBX_PLUGIN_DATA_DIR → 原样使用（宿主显式注入，未来方案 A 接入点）；
//  2. DBX_DATA_DIR → <DBX_DATA_DIR>/plugin-data/io.dbx.ldap
//     （便携/web 模式；用 plugin-data/ 避开安装器注册树）；
//  3. 平台标准用户数据目录下 dbx-plugin-data/io.dbx.ldap：
//     darwin → $HOME/Library/Application Support/...；
//     其余 unix → ${XDG_DATA_HOME:-$HOME/.local/share}/...；
//     windows → %APPDATA%\...；
//  4. 以上全缺（HOME 未设等）→ os.TempDir()/dbx-plugin-data/io.dbx.ldap
//     最后兜底，永不失败。
//
// 纯函数：环境查找与 GOOS 由参数注入，便于单测不依赖真实环境。
// 不使用 os.UserConfigDir()（Linux 上语义是 config 不是 data）。
func ResolveDataDir(getenv func(string) string, goos string) string {
	if v := strings.TrimSpace(getenv(EnvDataDir)); v != "" {
		return v
	}
	if v := strings.TrimSpace(getenv(EnvDataRoot)); v != "" {
		return filepath.Join(v, "plugin-data", DefaultDirName)
	}
	if home := platformDataHome(getenv, goos); home != "" {
		return filepath.Join(home, "dbx-plugin-data", DefaultDirName)
	}
	return filepath.Join(os.TempDir(), "dbx-plugin-data", DefaultDirName)
}

// platformDataHome 返回当前平台的用户数据根目录；不可用时返回空串。
func platformDataHome(getenv func(string) string, goos string) string {
	switch goos {
	case "darwin":
		home := strings.TrimSpace(getenv("HOME"))
		if home == "" {
			return ""
		}
		return filepath.Join(home, "Library", "Application Support")
	case "windows":
		return strings.TrimSpace(getenv("APPDATA"))
	default:
		if v := strings.TrimSpace(getenv("XDG_DATA_HOME")); v != "" {
			return v
		}
		home := strings.TrimSpace(getenv("HOME"))
		if home == "" {
			return ""
		}
		return filepath.Join(home, ".local", "share")
	}
}

// Open 打开插件数据目录（ResolveDataDir 统一解析，持久目录兜底）；
// 目录不存在则创建。
func Open() (*Store, error) {
	return OpenAt(ResolveDataDir(os.Getenv, runtime.GOOS))
}

// OpenAt 显式指定数据目录（测试/工具用）。
func OpenAt(dir string) (*Store, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("store: data dir is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("store: create data dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

// Dir 返回数据目录绝对路径。
func (s *Store) Dir() string {
	return s.dir
}

// --- 通用 JSON 文件（prefs.json / presets.json） ---

// LoadJSON 读取 dataDir/<name> 到 out；文件不存在时不修改 out 并返回 false。
func (s *Store) LoadJSON(name string, out any) (bool, error) {
	path := filepath.Join(s.dir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("store: read %s: %w", name, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return false, fmt.Errorf("store: parse %s: %w", name, err)
	}
	return true, nil
}

// SaveJSON 原子写入 dataDir/<name>（临时文件 + rename，权限 0600）。
func (s *Store) SaveJSON(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("store: encode %s: %w", name, err)
	}
	data = append(data, '\n')
	path := filepath.Join(s.dir, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("store: write %s: %w", name, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("store: rename %s: %w", name, err)
	}
	return nil
}

// --- prefs.json ---

// LoadPrefs 读取 prefs.json；不存在时返回空 map。
func (s *Store) LoadPrefs() (map[string]any, error) {
	prefs := map[string]any{}
	if _, err := s.LoadJSON("prefs.json", &prefs); err != nil {
		return nil, err
	}
	return prefs, nil
}

// SavePrefs 覆盖写入 prefs.json。
func (s *Store) SavePrefs(prefs map[string]any) error {
	if prefs == nil {
		prefs = map[string]any{}
	}
	return s.SaveJSON("prefs.json", prefs)
}

// --- audit.jsonl ---

// AuditRecord 是 audit.jsonl 的一行（M0 §4 格式，字段名与 ssh-sftp 对齐）。
type AuditRecord struct {
	Time         string `json:"time"`         // RFC3339
	ConnectionID string `json:"connectionId"` // 宿主连接 id
	Action       string `json:"action"`       // 如 ldap/entry/delete
	Target       string `json:"target"`       // DN（不记值）
	Result       string `json:"result"`       // ok | denied | error
}

// AppendAudit 追加一条审计记录；rec.Time 为空时取当前时间（RFC3339）。
// append-only，无锁文件写入依赖调用方串行化（sidecar 单进程内由审计回调串行）。
func (s *Store) AppendAudit(rec AuditRecord) error {
	if strings.TrimSpace(rec.Time) == "" {
		rec.Time = time.Now().Format(time.RFC3339)
	}
	if strings.TrimSpace(rec.Result) == "" {
		rec.Result = "ok"
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("store: encode audit record: %w", err)
	}
	path := filepath.Join(s.dir, "audit.jsonl")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("store: open audit.jsonl: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("store: append audit.jsonl: %w", err)
	}
	return nil
}

// ReadAuditLines 读取全部审计行（测试/排障用，主流程不使用）。
func (s *Store) ReadAuditLines() ([]AuditRecord, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, "audit.jsonl"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := []AuditRecord{}
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec AuditRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return out, fmt.Errorf("store: parse audit line: %w", err)
		}
		out = append(out, rec)
	}
	return out, nil
}

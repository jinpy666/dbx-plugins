// Package store 管理 DBX_PLUGIN_DATA_DIR 下的本地数据与审计
// （shared/IMPL_PLAN_M0_COMMON.zh-CN.md §4）。
//
//	$DBX_PLUGIN_DATA_DIR/            缺省 fallback: $TMPDIR/dbx-plugin-data/io.dbx.kafka
//	├── prefs.json                   UI 偏好（非敏感）
//	├── presets.json                 Kafka 消费/过滤预设（明文，不含凭据）
//	└── audit.jsonl                  写操作审计（append-only）
//
// 凭据红线：任何文件不落密码/私钥/token；审计记录 target 只记 topic/group
// 等资源名，不记消息值。
//
// 本包照抄 ldap/internal/store，仅 DefaultDirName 改为 io.dbx.kafka。
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultDirName 是无 DBX_PLUGIN_DATA_DIR 时的缺省目录名（对齐 M0 §4 fallback）。
const DefaultDirName = "io.dbx.kafka"

// EnvDataDir 是宿主注入的数据目录环境变量。
const EnvDataDir = "DBX_PLUGIN_DATA_DIR"

// Store 绑定一个数据目录。
type Store struct {
	dir string
}

// Open 打开插件数据目录：优先 DBX_PLUGIN_DATA_DIR，否则
// $TMPDIR/dbx-plugin-data/io.dbx.kafka；目录不存在则创建。
func Open() (*Store, error) {
	dir := strings.TrimSpace(os.Getenv(EnvDataDir))
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "dbx-plugin-data", DefaultDirName)
	}
	return OpenAt(dir)
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
	Action       string `json:"action"`       // 如 kafka/topics/delete
	Target       string `json:"target"`       // 资源名（topic/group/acl 描述，不记值）
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

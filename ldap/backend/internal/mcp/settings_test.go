package mcp

// settings_test.go：mcp/settings 加载/校验/持久化（设计 §2 骨架，
// 验收用例见 shared/frontend/README.zh-CN.md「MCP 两阶段/digest/cursor
// 验收用例清单」S-SET-*）。

import (
	"io"
	"testing"

	"io.dbx.ldap.plugin/internal/store"
)

func TestSettingsDefaultsAndClamp(t *testing.T) {
	settings := DefaultSettings()
	if settings.ReportWaitMs != 5000 || settings.CellWidth != 120 || settings.ResponseLimitBytes != 16*1024 {
		t.Fatalf("unexpected defaults: %+v", settings)
	}
	// 同族对齐字段默认值（files cursor 三件套 + kafka digestScanLimit）。
	if settings.DigestScanLimit != 1000 || settings.MaxCursorRows != 10000 ||
		settings.CursorTtlSecs != 600 || settings.MaxCursorSessions != 8 {
		t.Fatalf("unexpected cursor/scan defaults: %+v", settings)
	}
	// 越界值一律收敛回设计硬上限（损坏文件 / 手改文件不可放大限制）。
	clamped := Settings{
		ReportWaitMs:       999999,
		CellWidth:          0,
		DigestGroupLimit:   100,
		DigestTopN:         50,
		DigestSampleRows:   99,
		DigestRowLimit:     1000,
		DigestScanLimit:    1 << 20,
		MaxCursorRows:      1 << 21,
		CursorTtlSecs:      1 << 20,
		MaxCursorSessions:  999,
		ResponseLimitBytes: 1 << 30,
	}.Sanitized()
	if clamped.ReportWaitMs != 30000 || clamped.CellWidth != 1 ||
		clamped.DigestGroupLimit != 20 || clamped.DigestTopN != 10 ||
		clamped.DigestSampleRows != 5 || clamped.DigestRowLimit != 20 ||
		clamped.DigestScanLimit != 100000 || clamped.MaxCursorRows != 100000 ||
		clamped.CursorTtlSecs != 3600 || clamped.MaxCursorSessions != 32 ||
		clamped.ResponseLimitBytes != 1024*1024 {
		t.Fatalf("clamp mismatch: %+v", clamped)
	}
}

func TestSettingsLoadFallsBackOnMissingOrCorruptFile(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := LoadSettings(st); got != DefaultSettings() {
		t.Fatalf("missing file should fall back to defaults: %+v", got)
	}
	if err := st.SaveJSON(settingsFileName, map[string]any{"reportWaitMs": "bogus"}); err != nil {
		t.Fatal(err)
	}
	if got := LoadSettings(st); got.ReportWaitMs != 5000 {
		t.Fatalf("corrupt file should keep default reportWaitMs: %+v", got)
	}
}

func TestSettingsUpdateWhitelistAndClamp(t *testing.T) {
	settings := DefaultSettings()
	updated, err := applySettingsUpdate(settings, map[string]any{
		"reportWaitMs": float64(300),
		"cellWidth":    float64(80),
		"unknownField": float64(1), // 白名单外字段静默忽略
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ReportWaitMs != 300 || updated.CellWidth != 80 {
		t.Fatalf("partial update mismatch: %+v", updated)
	}
	if _, err := applySettingsUpdate(updated, map[string]any{"reportWaitMs": float64(0)}); err == nil {
		t.Fatal("zero/negative must be rejected")
	}
	if _, err := applySettingsUpdate(updated, map[string]any{"digestGroupLimit": float64(21)}); err == nil {
		t.Fatal("above ceiling must be rejected")
	}
	if _, err := applySettingsUpdate(updated, map[string]any{"responseLimitBytes": "big"}); err == nil {
		t.Fatal("non-numeric must be rejected")
	}
	// 拒绝不污染当前值：再次 set 合法值仍然成功。
	if _, err := applySettingsUpdate(updated, map[string]any{"digestRowLimit": float64(5)}); err != nil {
		t.Fatal(err)
	}
	// 同族对齐字段在白名单内可 set（字符串数字变体同样接受），越界拒绝。
	tuned, err := applySettingsUpdate(updated, map[string]any{
		"digestScanLimit":   "500",
		"maxCursorRows":     float64(5000),
		"cursorTtlSecs":     float64(120),
		"maxCursorSessions": float64(4),
	})
	if err != nil {
		t.Fatal(err)
	}
	if tuned.DigestScanLimit != 500 || tuned.MaxCursorRows != 5000 ||
		tuned.CursorTtlSecs != 120 || tuned.MaxCursorSessions != 4 {
		t.Fatalf("cursor/scan tuning mismatch: %+v", tuned)
	}
	if _, err := applySettingsUpdate(updated, map[string]any{"maxCursorSessions": float64(33)}); err == nil {
		t.Fatal("maxCursorSessions above ceiling must be rejected")
	}
	if _, err := applySettingsUpdate(updated, map[string]any{"cursorTtlSecs": "long"}); err == nil {
		t.Fatal("non-numeric cursorTtlSecs must be rejected")
	}
}

func TestSettingsPersistRoundtrip(t *testing.T) {
	dir := t.TempDir()
	st, err := store.OpenAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	settings := DefaultSettings()
	settings.ReportWaitMs = 250
	if err := SaveSettings(st, settings); err != nil {
		t.Fatal(err)
	}
	if got := LoadSettings(st); got.ReportWaitMs != 250 || got != settings {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
	// nil store（数据目录不可用降级）：Save 静默、Load 回默认。
	if err := SaveSettings(nil, settings); err != nil {
		t.Fatalf("nil store save should be a no-op: %v", err)
	}
	if got := LoadSettings(nil); got != DefaultSettings() {
		t.Fatalf("nil store load should return defaults: %+v", got)
	}
	_ = io.Discard
}

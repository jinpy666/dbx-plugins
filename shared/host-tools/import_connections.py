#!/usr/bin/env python3
"""DBX 连接导入 + 环境颜色规范工具 —— 纯数据变换器（零 sqlite3 / 零 subprocess）。

把 mcpctl 体系里的 SSH / Kafka 连接导入 DBX 桌面 UI（dbx.db connections 表），
并按环境规范给全部连接上色：PROD 红、UAT/STAGING 黄、SIT 灰、其余默认。

模板来源（dbx.db 现网样本）：
  SSH   = vagrant 连接（db_type=plugin, plugin_id=io.dbx.ssh），
          quick_sudo=true 即接入 KN Quick Sudo
  Kafka = Apache Kafka_qf18 连接（db_type=mq, driver_profile=kafka, SASL_SSL）

标准流程：
  # 1. 从 mcpctl 导出 profile（只读）
  mcpctl --agent --format data profile list --type ssh   > mcpctl_ssh.json
  mcpctl --agent --format data profile list --type kafka > mcpctl_kafka.json
  sqlite3 -json "file:$DBX_APP_DATA/dbx.db?mode=ro" "SELECT id, config_json FROM connections;" > connections.json

  # 2. 预览 / 生成
  python3 import_connections.py --ssh mcpctl_ssh.json --kafka mcpctl_kafka.json \
      --existing connections.json --current-layout current-layout.json \
      [--plan | --gen outdir]

  # 3. 写入（必须先完全退出 DBX.app；工具产物对写库动作可见）
  cp dbx.db dbx.db.backup-$(date +%Y%m%d-%H%M%S)
  sqlite3 dbx.db < outdir/insert-connections.sql
  sqlite3 dbx.db < outdir/color-update.sql
  sqlite3 dbx.db < outdir/layout-update.sql

注意：layout_json 必须 CAST AS TEXT 写入（readfile 返回 BLOB 会让宿主读取失败）。
Kafka SASL 密码 mcpctl 侧 redacted，导入后留空，首次连接时在 UI 输入并保存。
"""

import argparse
import json
import uuid
from pathlib import Path

COLOR_PROD = "#ef4444"    # 红（宿主预设 bg-red-500）
COLOR_UAT = "#eab308"     # 黄（宿主预设 bg-yellow-500）
COLOR_SIT = "#6b7280"     # 灰（宿主无灰色预设，取 gray-500 自定义色）

SSH_TEMPLATE_LABEL = "SSH 服务器"
KAFKA_TEMPLATE_LABEL = "Apache Kafka"

# mcpctl SSH group → DBX UI 分组名（None 表示新建同名组）
SSH_GROUP_MAP = {
    "ACCT Data Pool": "ACCT",
    "OMNI API": "OMNI",
    "default": "个人与临时",
}

# kafka profile 名 → 目标分组
KAFKA_GROUP_MAP = {
    "datapool-int": "ACCT",
    "tss-int": "TSS",
    "tss-prod": "TSS",
}

# 组追加顺序：新组插在 "本地与基础设施" 之前
ANCHOR_GROUP = "本地与基础设施"

SQL_UPSERT_LAYOUT = (
    "INSERT INTO sidebar_layout (id, layout_json) VALUES (?, ?) "
    "ON CONFLICT(id) DO UPDATE SET layout_json = excluded.layout_json"
)


def env_color(name, tags=()):
    """环境颜色规范：UAT/STAGING 黄、SIT 灰、PROD 红、其余默认。UAT 优先于 PROD。"""
    text = " ".join([name] + [str(t) for t in tags]).upper()
    if "UAT" in text or "STAGING" in text:
        return COLOR_UAT
    if "SIT" in text:
        return COLOR_SIT
    if "PROD" in text:
        return COLOR_PROD
    return ""


def base_config(cid, name, color):
    """connections.config_json 的公共骨架（对齐现网样本键集）。"""
    return {
        "id": cid,
        "name": name,
        "url_params": "",
        "password": "",
        "database": None,
        "color": color,
        "connect_timeout_secs": 10,
        "query_timeout_secs": 60,
        "idle_timeout_secs": 60,
        "keepalive_interval_secs": 30,
        "ssl": False,
        "sysdba": False,
        "connection_string": None,
        "jdbc_driver_class": None,
        "jdbc_driver_paths": [],
        "save_password": False,
    }


def ssh_config(profile):
    """mcpctl ssh profile → DBX SSH 插件连接（quick_sudo=true 接入 KN Quick Sudo）。"""
    name = profile["name"]
    color = env_color(name, profile.get("tags") or [])
    cfg = base_config(str(uuid.uuid4()), name, color)
    cfg.update({
        "db_type": "plugin",
        "driver_profile": "plugin",
        "driver_label": SSH_TEMPLATE_LABEL,
        "host": profile.get("host", ""),
        "port": int(profile.get("port") or 22),
        "username": profile.get("username", ""),
        "external_config": {
            "authentication": "private-key",
            "private_key_path": profile.get("pkFile") or "",
            "agent_socket": "",
            "connect_timeout_secs": 15,
            "keepalive_interval_secs": 30,
            "quick_sudo": True,
            "auth_flow_mode": "password_then_otp",
            "password_prompt_hint": "",
            "totp_prompt_hint": "",
            "sudo_use_pty": False,
            "read_only": False,
        },
        "plugin_id": "io.dbx.ssh",
        "plugin_connection_provider": "io.dbx.ssh.connection",
        "plugin_connection_type": "ssh",
        # key 认证没有可保存的登录密码；save_password=False 会让宿主在连接前
        # 弹登录密码框（key 连接无从应答），必须为 True。
        "save_password": True,
    })
    return cfg


def kafka_config(profile):
    """mcpctl kafka profile → DBX mq 连接。密码不可导出，留空待 UI 首连输入。"""
    name = profile["name"]
    brokers = profile.get("bootstrapServers") or []
    first = brokers[0] if brokers else ""
    host, _, port = first.rpartition(":")
    port = int(port) if port.isdigit() else 9093
    sasl = profile.get("sasl") or {}
    username = sasl.get("username", "")
    color = env_color(name)
    cfg = base_config(str(uuid.uuid4()), name, color)
    cfg.update({
        "db_type": "mq",
        "driver_profile": "kafka",
        "driver_label": KAFKA_TEMPLATE_LABEL,
        "host": host,
        "port": port,
        "username": username,
        "connect_timeout_secs": 30,
        "query_timeout_secs": 30,
        "ssl": True,
        "external_config": {
            "systemKind": "kafka",
            "adminUrl": "",
            "auth": {"kind": "basic", "username": username, "password": ""},
            "tlsSkipVerify": True,
            "extra": {
                "bootstrapServers": first,
                "securityProtocol": "SASL_SSL" if (profile.get("transport", {}).get("tls", {}) or {}).get("enable") else "PLAINTEXT",
                "saslMechanism": sasl.get("mechanism", "PLAIN"),
            },
        },
        "database_info": {"productName": "Kafka", "driverName": "Message Queue Admin API"},
    })
    return cfg


def dedupe_names(configs):
    """同名连接追加 " (组名)" 消歧（SSH 同一 host 常挂多个业务组）。"""
    seen = {}
    for cfg in configs:
        name = cfg["name"]
        if name in seen:
            seen[name] += 1
            cfg["name"] = f"{name} ({cfg.pop('_group', '')})" if cfg.get("_group") else f"{name} ({seen[name]})"
        else:
            seen[name] = 1
    return configs


def build_layout(existing_layout, mapping_additions):
    """在现有布局上追加新连接：mapping_additions = {组名: [ {id,name}, ... ]}。
    新组插在 ANCHOR_GROUP 之前，已有组直接追加 children（DB 连接在前、导入在后）。"""
    layout = json.loads(json.dumps(existing_layout))
    groups_by_id = {g["id"]: g for g in layout["groups"]}
    entries_by_id = {}
    for entry in layout["order"]:
        if entry.get("type") == "group":
            entries_by_id[entry["id"]] = entry

    new_groups = [g for g in mapping_additions if g["name"] not in
                  {x["name"] for x in layout["groups"]}]
    for name in new_groups_order(new_groups):
        gid = uuid.uuid4().hex
        group = {"id": gid, "name": name, "collapsed": False}
        layout["groups"].append(group)
        entries_by_id[gid] = {"type": "group", "id": gid, "children": []}
        anchor_idx = next((i for i, e in enumerate(layout["order"])
                           if e.get("type") == "group"
                           and groups_by_id.get(e["id"], {}).get("name") == ANCHOR_GROUP), None)
        if anchor_idx is None:
            layout["order"].append(entries_by_id[gid])
        else:
            layout["order"].insert(anchor_idx, entries_by_id[gid])

    for item in mapping_additions:
        target = item["name"]
        gid = next(g["id"] for g in layout["groups"] if g["name"] == target)
        entry = entries_by_id[gid]
        entry.setdefault("children", []).extend(
            {"type": "connection", "id": c["id"]} for c in item["connections"])
    return layout


def new_groups_order(new_groups):
    """新组按声明顺序稳定排列。"""
    return [g["name"] for g in new_groups]


def sql_quote(s):
    return "'" + str(s).replace("'", "''") + "'"


def main():
    ap = argparse.ArgumentParser(description="DBX 连接导入 + 环境颜色工具（纯变换器）")
    ap.add_argument("--ssh", type=Path, help="mcpctl ssh profiles JSON")
    ap.add_argument("--kafka", type=Path, help="mcpctl kafka profiles JSON")
    ap.add_argument("--existing", type=Path, required=True, help="connections.json（现有连接导出）")
    ap.add_argument("--current-layout", type=Path, help="当前 layout JSON")
    ap.add_argument("--plan", action="store_true", help="只读预览")
    ap.add_argument("--gen", type=Path, help="生成目录")
    args = ap.parse_args()

    existing_rows = json.loads(args.existing.read_text(encoding="utf-8"))
    existing_names = {json.loads(r["config_json"]).get("name", "") for r in existing_rows}
    existing_ids = {r["id"] for r in existing_rows}

    new_configs = []
    additions_index = {}  # 组名 → [config]

    def add_to_group(group, cfg):
        additions_index.setdefault(group, []).append(cfg)
        new_configs.append(cfg)

    if args.ssh:
        profiles = json.loads(args.ssh.read_text(encoding="utf-8"))
        for p in profiles.get("profiles", profiles):
            name = p.get("name", "")
            if name == "vagrant" or name in existing_names:
                continue  # vagrant 等已在库中
            cfg = ssh_config(p)
            cfg["_group"] = SSH_GROUP_MAP.get(p.get("group"), p.get("group") or "个人与临时")
            add_to_group(cfg["_group"], cfg)

    if args.kafka:
        profiles = json.loads(args.kafka.read_text(encoding="utf-8"))
        merged = {}
        for p in profiles.get("profiles", profiles):
            # mcpctl 里 datapool-int 有两个重复 profile，合并为一个
            merged.setdefault(p.get("name"), p)
        for name, p in merged.items():
            if name in existing_names:
                continue
            cfg = kafka_config(p)
            cfg["_group"] = KAFKA_GROUP_MAP.get(name, "本地与基础设施")
            add_to_group(cfg["_group"], cfg)

    dedupe_names(new_configs)
    for cfg in new_configs:
        cfg.pop("_group", None)

    mapping_additions = [
        {"name": group, "connections": [{"id": c["id"], "name": c["name"]} for c in cfgs]}
        for group, cfgs in additions_index.items()
    ]

    print(f"现有连接：{len(existing_rows)}，本次新增：{len(new_configs)}")
    for group, cfgs in additions_index.items():
        colors = {"PROD红": 0, "UAT黄": 0, "SIT灰": 0}
        for c in cfgs:
            if c["color"] == COLOR_PROD:
                colors["PROD红"] += 1
            elif c["color"] == COLOR_UAT:
                colors["UAT黄"] += 1
            elif c["color"] == COLOR_SIT:
                colors["SIT灰"] += 1
        names = ", ".join(c["name"] for c in cfgs)
        print(f"  → [{group}]: {names}  (颜色 红{colors['PROD红']}/黄{colors['UAT黄']}/灰{colors['SIT灰']})")

    # 颜色规范对现有连接的生效情况
    color_hits = {"PROD红": [], "UAT黄": [], "SIT灰": []}
    for r in existing_rows:
        cfg = json.loads(r["config_json"])
        c = env_color(cfg.get("name", ""))
        if c == COLOR_PROD:
            color_hits["PROD红"].append(cfg["name"])
        elif c == COLOR_UAT:
            color_hits["UAT黄"].append(cfg["name"])
        elif c == COLOR_SIT:
            color_hits["SIT灰"].append(cfg["name"])
    for k, v in color_hits.items():
        print(f"  现有连接 {k} ×{len(v)}: {', '.join(v)}")

    if args.plan or not args.gen:
        print("\n仅预览。加 --gen <outdir> 生成写入 SQL。")
        return 0

    out = args.gen
    out.mkdir(parents=True, exist_ok=True)

    # 1. 新连接 INSERT
    lines = ["-- 由 import_connections.py 生成：导入 SSH/Kafka 插件连接"]
    for cfg in new_configs:
        blob = json.dumps(cfg, ensure_ascii=False)
        lines.append(
            f"INSERT INTO connections (id, config_json) VALUES ({sql_quote(cfg['id'])}, {sql_quote(blob)});")
    (out / "insert-connections.sql").write_text("\n".join(lines) + "\n", encoding="utf-8")

    # 2. 颜色 UPDATE（现有 + 新增全量，幂等）
    lines = ["-- 由 import_connections.py 生成：环境颜色规范（PROD红/UAT黄/SIT灰）"]
    for r in existing_rows:
        cfg = json.loads(r["config_json"])
        color = env_color(cfg.get("name", ""))
        if cfg.get("color") != color:
            lines.append(
                f"UPDATE connections SET config_json = json_set(config_json, '$.color', {sql_quote(color)}) "
                f"WHERE id = {sql_quote(r['id'])};")
    (out / "color-update.sql").write_text("\n".join(lines) + "\n", encoding="utf-8")

    # 3. 布局重建（现有布局 + 新组新连接；新连接的颜色已在 config 内）
    if args.current_layout:
        old_layout = json.loads(args.current_layout.read_text(encoding="utf-8"))
        # 沿用原组内顺序
        old_order = []

        def walk(entries):
            for e in entries or []:
                if e.get("type") == "connection":
                    old_order.append(e.get("id"))
                else:
                    walk(e.get("children"))
        walk(old_layout.get("order"))
        rank = {cid: i for i, cid in enumerate(old_order)}
        for group, cfgs in additions_index.items():
            cfgs.sort(key=lambda c: 0)  # 保持导入顺序即可
        layout = build_layout(old_layout, mapping_additions)
        layout_file = out / "layout.json"
        layout_file.write_text(json.dumps(layout, ensure_ascii=False, indent=2), encoding="utf-8")
        layout_sql = (
            "-- 由 import_connections.py 生成：重建侧边栏布局（含导入连接）\n"
            "-- CAST AS TEXT 必须：readfile 返回 BLOB 会导致宿主读取失败\n"
            f"UPDATE sidebar_layout SET layout_json = CAST(readfile('{layout_file.resolve()}') AS TEXT) WHERE id = 1;\n")
        (out / "layout-update.sql").write_text(layout_sql, encoding="utf-8")
        print(f"\n已生成：{out}/(insert-connections|color-update|layout-update).sql + layout.json")

    return 0


if __name__ == "__main__":
    import sys
    sys.exit(main())

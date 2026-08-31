#!/usr/bin/env python3
"""DBX 桌面 UI 侧边栏分组（目录）导入工具 —— 纯数据变换器。

本脚本不直接连接数据库（零 sqlite3 / 零 subprocess）；数据的读取与写回
全部通过 sqlite3 命令行显式完成，因此所有写库动作对操作者可见、可控。

背景：DBX 宿主没有提供分组管理的 MCP/CLI 接口，UI 侧边栏分组只存于
app-data 的 dbx.db → sidebar_layout.layout_json（结构见宿主前端
apps/desktop/src/types/database.ts 的 SidebarLayout / SidebarOrderEntry）：

    {
      "groups": [ {"id": "<uuid>", "name": "<组名>", "collapsed": false}, ... ],
      "order":  [ {"type": "group", "id": "<组id>", "children": [ {"type": "connection", "id": "<连接id>"}, ... ]},
                  {"type": "connection", "id": "<连接id>"}, ... ]
    }

标准流程（DBX_APP_DATA=~/Library/Application Support/com.dbx.app）：

  # 1. 只读导出连接清单（config_json 内含 name 字段）
  sqlite3 -json "file:$DBX_APP_DATA/dbx.db?mode=ro" \\
    "SELECT id, config_json FROM connections;" > connections.json

  # 2. 预览分组方案（只读）
  python3 sidebar_groups.py --from-json connections.json --plan

  # 3. 生成 layout.json 与写入 SQL
  python3 sidebar_groups.py --from-json connections.json --gen out_dir/

  # 4. 备份后写入（必须先完全退出 DBX.app）
  cp "$DBX_APP_DATA/dbx.db" "$DBX_APP_DATA/dbx.db.backup-$(date +%Y%m%d-%H%M%S)"
  sqlite3 "$DBX_APP_DATA/dbx.db" < out_dir/update.sql

  # 5. 启动 DBX.app 验证侧边栏；异常时用 out_dir/rollback.sql 还原

映射表可用 --export-mapping 导出、编辑后经 --mapping 传入。
"""

import argparse
import json
import uuid
from pathlib import Path

# 内置映射：连接名 → 组。连接名必须与 connections.config_json 的 name 完全一致。
# 组按声明顺序排列；调整分组直接改这个表或用 --export-mapping 导出后编辑。
BUILTIN_MAPPING = {
    "ACCT": ["ACCT-PROD", "ACCT-SIT", "ACCT-UAT"],
    "ACL": ["ACL-PROD", "ACL-TEST", "ACL-UAT（代替PROD）"],
    "CLPrint": ["CLPrint-PROD"],
    "eCourier": ["eCourier-PROD", "eCourier-PROD2", "eCourier-UAT"],
    "FRTINV": ["FRTINV-PROD", "FRTINV-SIT"],
    "GW": ["GW-PROD", "GW-TEST"],
    "IDP": ["IDP-PROD", "IDP-TEST"],
    "MSG": ["MSG-PROD", "MSG-TEST"],
    "nTree": ["nTree-PROD", "nTree-TEST"],
    "Nokia DMS": ["nokia-dms-live", "nokia-dms-uat", "docker-oracle19c-nokia"],
    "SHA": ["SHA-AF", "SHA-MI", "shaboms15.corp.int.kn"],
    "Skynet": ["Skynet-DEV", "Skynet-DEV2", "Skynet-PROD", "Skynet-TEST", "SkynetNext-DEV"],
    "sml": ["sml-prod"],
    "Survey": ["Survey-PROD", "Survey-UAT"],
    "TSS": ["TSS-DEV", "TSS-PROD", "TSS-SIT"],
    "Track": ["Track-PROD", "Track-UAT"],
    "Ucenter": ["Ucenter-PROD", "Ucenter-TEST", "ucenter-prod-mm"],
    "OMNI": ["myomnit1-uat"],
    "本地与基础设施": [
        "docker-oracle19c-system", "docker-postgres", "local_mysql", "local_ubuntu",
        "hk392", "sentry", "SPUG", "RDS", "Storage", "LDAP server",
        "SQL Server_7985", "Apache Kafka_qf18", "Alicloud-HK",
    ],
    "个人与临时": ["vagrant", "kn_laptop_redhat", "Wolf-posg", "w_egmao_hk", "wf-tencent", "zbw", "zbw-wf"],
}

SQL_UPDATE_TEMPLATE = """-- 由 sidebar_groups.py 生成：将 layout_json 置为 {layout_file}
UPDATE sidebar_layout SET layout_json = readfile('{layout_file}') WHERE id = 1;
"""

SQL_ROLLBACK_TEMPLATE = """-- 由 sidebar_groups.py 生成：回滚到备份的 layout_json（{backup_note}）
UPDATE sidebar_layout SET layout_json = readfile('{backup_file}') WHERE id = 1;
"""


def load_connections_json(path: Path):
    """读取 sqlite3 -json 导出的连接清单，返回 (id, name) 列表。"""
    rows = json.loads(path.read_text(encoding="utf-8"))
    result = []
    for row in rows:
        config = json.loads(row["config_json"])
        result.append((row["id"], config.get("name", "")))
    return result


def validate_mapping(mapping, connections):
    """校验映射与连接清单一致：映射名必须存在、连接尽量全覆盖、不允许重复。"""
    known_names = {name for _, name in connections}
    mapped = [name for names in mapping.values() for name in names]

    missing_in_db = sorted(set(mapped) - known_names)
    unmapped = sorted(known_names - set(mapped))
    dupes = sorted({n for n in mapped if mapped.count(n) > 1})
    return missing_in_db, unmapped, dupes


def current_connection_order(layout):
    """提取现有 layout 的连接展示顺序，编组时尽量沿用。"""
    order_seq = []

    def walk(entries):
        for entry in entries or []:
            if entry.get("type") == "connection":
                order_seq.append(entry.get("id"))
            else:
                walk(entry.get("children"))

    walk((layout or {}).get("order"))
    return order_seq


def build_layout(mapping, connections, old_layout):
    """按映射生成 SidebarLayout。组内沿用现有展示顺序，其余按清单顺序补齐。"""
    order_seq = current_connection_order(old_layout)
    name_to_id = {name: cid for cid, name in connections}
    rank = {cid: i for i, cid in enumerate(order_seq)}

    groups, order = [], []
    grouped_ids = set()
    for group_name, names in mapping.items():
        gid = uuid.uuid4().hex
        groups.append({"id": gid, "name": group_name, "collapsed": False})
        children = [
            {"type": "connection", "id": name_to_id[n]}
            for n in sorted(names, key=lambda n: rank.get(name_to_id[n], 1 << 30))
        ]
        grouped_ids.update(name_to_id[n] for n in names)
        order.append({"type": "group", "id": gid, "children": children})

    # 未编组连接按原顺序追加在顶层，保持平铺
    for cid, _name in connections:
        if cid not in grouped_ids:
            order.append({"type": "connection", "id": cid})
    return {"groups": groups, "order": order}


def print_plan(layout, connections):
    name_by_id = {cid: n for cid, n in connections}
    total_grouped = 0
    print(f"编组数：{len(layout['groups'])}")
    for g in layout["groups"]:
        entry = next(e for e in layout["order"] if e.get("id") == g["id"])
        names = [name_by_id.get(c["id"], c["id"]) for c in entry["children"]]
        total_grouped += len(names)
        print(f"  [{g['name']}] ({len(names)}): {', '.join(names)}")
    flat = [c["id"] for c in layout["order"] if c.get("type") == "connection"]
    if flat:
        print(f"  [顶层平铺] ({len(flat)}): {', '.join(name_by_id.get(i, i) for i in flat)}")
    print(f"连接总数：{len(connections)}，编组 {total_grouped} 个，顶层平铺 {len(flat)} 个")


def main():
    ap = argparse.ArgumentParser(description="DBX UI 侧边栏分组导入工具（纯变换器，不直接碰库）")
    ap.add_argument("--from-json", type=Path, required=True, help="sqlite3 -json 导出的连接清单")
    ap.add_argument("--mapping", type=Path, help="映射表 JSON（组名 → 连接名数组），缺省用内置映射")
    ap.add_argument("--current-layout", type=Path, help="当前 layout_json 备份（用于组内沿用原顺序）")
    ap.add_argument("--plan", action="store_true", help="只读预览分组方案")
    ap.add_argument("--export-mapping", type=Path, help="导出内置映射表为 JSON")
    ap.add_argument("--gen", type=Path, help="生成目录：layout.json / update.sql / rollback.sql")
    ap.add_argument("--restore-from", type=Path, help="用备份的 layout JSON 生成回滚 SQL（配 --gen）")
    args = ap.parse_args()

    if args.export_mapping:
        args.export_mapping.write_text(
            json.dumps(BUILTIN_MAPPING, ensure_ascii=False, indent=2), encoding="utf-8")
        print(f"已导出映射表：{args.export_mapping}")
        return 0

    connections = load_connections_json(args.from_json)

    mapping = BUILTIN_MAPPING
    if args.mapping:
        mapping = json.loads(args.mapping.read_text(encoding="utf-8"))

    missing_in_db, unmapped, dupes = validate_mapping(mapping, connections)
    if missing_in_db:
        print(f"警告：映射中的连接名不在清单里（将忽略）：{missing_in_db}", file=sys.stderr)
    if dupes:
        print(f"错误：映射重复：{dupes}", file=sys.stderr)
        return 1
    if unmapped:
        print(f"提示：以下连接未编组，保持顶层平铺：{unmapped}")

    old_layout = None
    if args.current_layout:
        old_layout = json.loads(args.current_layout.read_text(encoding="utf-8"))
    layout = build_layout(mapping, connections, old_layout)

    if args.plan or not args.gen:
        print_plan(layout, connections)
        print("\n仅预览，未生成文件。确认后加 --gen <out_dir> 生成写入 SQL。")
        return 0

    out_dir = args.gen
    out_dir.mkdir(parents=True, exist_ok=True)
    layout_file = out_dir / "layout.json"
    layout_file.write_text(json.dumps(layout, ensure_ascii=False, indent=2), encoding="utf-8")

    (out_dir / "update.sql").write_text(
        SQL_UPDATE_TEMPLATE.format(layout_file=str(layout_file.resolve())), encoding="utf-8")

    if args.restore_from:
        backup_file = out_dir / "layout.backup.json"
        backup_file.write_text(
            json.dumps(json.loads(args.restore_from.read_text(encoding="utf-8")),
                       ensure_ascii=False, indent=2), encoding="utf-8")
        (out_dir / "rollback.sql").write_text(
            SQL_ROLLBACK_TEMPLATE.format(
                backup_file=str(backup_file.resolve()), backup_note=args.restore_from.name),
            encoding="utf-8")

    print_plan(layout, connections)
    print(f"\n已生成：{layout_file}")
    print(f"已生成：{out_dir / 'update.sql'}（sqlite3 <dbx.db> < update.sql 执行）")
    if args.restore_from:
        print(f"已生成：{out_dir / 'rollback.sql'}")
    return 0


if __name__ == "__main__":
    import sys
    sys.exit(main())

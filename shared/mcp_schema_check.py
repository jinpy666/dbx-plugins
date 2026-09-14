#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
mcp_schema_check.py — DBX 插件族 MCP schema↔行为一致性核对器（MCP 专项第五轮）

只读核对：spawn sidecar `--mcp`，完成 initialize + tools/list 后，对每个工具：
  A+) 严格宿主透镜（第六轮）：对 tools/list 原始信封按严格 MCP 宿主（zcode
     zod）口径核对——required 缺席或字符串数组（null 即整服务器被拒收）、
     name/description/inputSchema.type/properties 形状；
  A) inputSchema 静态校验：属性 type 合法、required ⊆ properties、enum 非空且互异、
     工具级/参数级 description 非空、工具名 `<域>_<动作>`、参数名 camelCase、
     草案外关键字（提示级）。
  B) 行为探针（离线安全——校验类错误发生在连接/拨号之前）：
     1. 空参数 {} → 应报缺参错误；错误点名集合与 schema required 比对；
     2. required 参数逐个传类型错误值（string←123、number/integer←"abc"、
        boolean←"abc"）→ 错误必须点名该参数；静默接受 = 漂移。
        注意：数字字符串对 number 字段合法（MCP_ACCEPTANCE §3.1），探针用
        非数字串 "abc" 避免误报；
     3. required enum 参数传非法值 → 错误应列出合法值且与 schema enum 一致；
        大小写归一（§3.3）属合法设计；
     4. 未知参数传入 → 只验证不崩（§3.5 静默忽略是设计，不算漂移）。
  调用超过 --probe-timeout（默认 5s）视为意外触发连接级执行：跳过并记
  probe-not-applicable，随后重启 sidecar 保证后续探针状态干净。

发现分级：
  DRIFT — schema 与行为矛盾（计入退出码）；
  WARN  — 与 MCP_ACCEPTANCE 登记设计一致但需人工复核语义方向 / 探针不适用；
  HINT  — 提示级（草案外关键字等），不影响退出码。

用法：
  python3 shared/mcp_schema_check.py --binary <sidecar> [--plugin io.dbx.xxx]
                                    [--probe-timeout 5] [--debug] [--only <tool>]
退出码：0 = 无漂移；1 = 存在漂移。
"""

import argparse
import json
import os
import queue
import re
import subprocess
import sys
import threading
import time

VALID_TYPES = {"string", "number", "integer", "boolean", "array", "object", "null"}

TOOL_NAME_RE = re.compile(r"^[a-z][a-z0-9]*(_[a-z][a-z0-9]*)+$")
PARAM_NAME_RE = re.compile(r"^[a-z][A-Za-z0-9]*$")

# JSON Schema draft-07 / 2020-12 常见关键字（MCP 客户端大概率认识的白名单）
KNOWN_SCHEMA_KEYWORDS = {
    "$schema", "$id", "$ref", "$defs", "definitions", "$anchor", "$comment",
    "$dynamicRef", "$dynamicAnchor", "id",
    "type", "enum", "const", "multipleOf", "maximum", "exclusiveMaximum",
    "minimum", "exclusiveMinimum", "maxLength", "minLength", "pattern",
    "format", "maxItems", "minItems", "uniqueItems", "maxContains",
    "minContains", "maxProperties", "minProperties", "required",
    "dependentRequired", "dependentSchemas", "properties", "patternProperties",
    "additionalProperties", "propertyNames", "unevaluatedProperties",
    "unevaluatedItems", "contains", "items", "prefixItems", "allOf", "anyOf",
    "oneOf", "not", "if", "then", "else",
    "title", "description", "default", "deprecated", "readOnly", "writeOnly",
    "examples", "contentEncoding", "contentMediaType", "contentSchema",
}

INVALID_ENUM_PROBE = "__mcp_schema_check_invalid_enum__"
UNKNOWN_PARAM_PROBE = "__mcp_schema_check_unknown__"

# 连接参数门特征：错误焦点在 connectionId/host/brokers 等连接参数解析，
# 业务参数校验尚未到达（§3.7 校验顺序张力，四插件共性发现，归 WARN 不算 schema 漂移）
CONNECTION_GATE_RE = re.compile(
    r"connection parameters required"
    r"|Missing required parameter: connectionId"
    r"|Unknown connectionId"
    r"|Missing or invalid parameter: host"
    r"|DBX app bridge",
    re.IGNORECASE)

MAX_EVIDENCE = 600  # 证据文本截断长度


def truncate(text, limit=MAX_EVIDENCE):
    s = text if isinstance(text, str) else json.dumps(text, ensure_ascii=False)
    return s if len(s) <= limit else s[:limit] + "...<truncated>"


def to_snake(name):
    return re.sub(r"(?<!^)(?=[A-Z])", "_", name).lower()


def name_variants(name):
    vs = [name]
    sn = to_snake(name)
    if sn != name:
        vs.append(sn)
    return vs


def mentions(param, message):
    """错误消息是否点名该参数（camelCase 原样 + snake_case 变体）。
    用字母数字下划线词边界匹配，避免短参数名（如 n）误命中普通单词。"""
    for v in name_variants(param):
        if re.search(r"(?<![A-Za-z0-9_])" + re.escape(v) + r"(?![A-Za-z0-9_])", message):
            return True
    return False


class Sidecar:
    """管理 sidecar 子进程：行式 JSON-RPC over stdio；超时后可重启重置状态。"""

    def __init__(self, binary, debug=False):
        self.binary = binary
        self.debug = debug
        self.proc = None
        self.q = None
        self._id = 0

    def start(self):
        self.stop()
        self.proc = subprocess.Popen(
            [self.binary, "--mcp"],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=None if self.debug else subprocess.DEVNULL,
            text=True,
            bufsize=1,
        )
        self.q = queue.Queue()
        threading.Thread(target=self._reader, daemon=True).start()
        self._id = 0

    def stop(self):
        if self.proc is not None and self.proc.poll() is None:
            try:
                self.proc.kill()
            except OSError:
                pass
        self.proc = None

    def _reader(self):
        try:
            for line in self.proc.stdout:
                line = line.strip()
                if line:
                    self.q.put(line)
        except (ValueError, OSError):
            pass
        self.q.put(None)  # EOF 哨兵

    def _send(self, payload):
        try:
            self.proc.stdin.write(json.dumps(payload, ensure_ascii=False) + "\n")
            self.proc.stdin.flush()
            return True
        except (BrokenPipeError, OSError, ValueError):
            return False

    def notify(self, method, params=None):
        msg = {"jsonrpc": "2.0", "method": method}
        if params is not None:
            msg["params"] = params
        self._send(msg)

    def request(self, method, params, timeout):
        """返回 (status, response)；status ∈ {ok, timeout, dead}"""
        if self.proc is None or self.proc.poll() is not None:
            self.start()
        self._id += 1
        rid = self._id
        if not self._send({"jsonrpc": "2.0", "id": rid, "method": method, "params": params}):
            # 写失败大概率进程已死：重启重发一次
            self.start()
            self._id += 1
            rid = self._id
            if not self._send({"jsonrpc": "2.0", "id": rid, "method": method, "params": params}):
                return ("dead", None)
        deadline = time.time() + timeout
        while True:
            remain = deadline - time.time()
            if remain <= 0:
                return ("timeout", None)
            try:
                item = self.q.get(timeout=remain)
            except queue.Empty:
                return ("timeout", None)
            if item is None:
                return ("dead", None)
            try:
                msg = json.loads(item)
            except json.JSONDecodeError:
                continue  # 非 JSON 行（日志污染等）忽略
            if isinstance(msg, dict) and msg.get("id") == rid:
                return ("ok", msg)
            # 其他 id 的迟到响应/通知：丢弃（超时后我们会重启进程，不会串线）


class Report:
    def __init__(self):
        self.drifts = []
        self.warns = []
        self.hints = []
        self.not_applicable = 0
        self.pass_tools = 0
        self.tool_count = 0

    def drift(self, tool, param, schema_says, behavior, request, response):
        self.drifts.append(dict(tool=tool, param=param, schema=schema_says,
                                behavior=behavior, request=request, response=response))

    def warn(self, tool, param, note, request=None, response=None):
        self.warns.append(dict(tool=tool, param=param, note=note,
                               request=request, response=response))

    def hint(self, tool, note):
        self.hints.append(dict(tool=tool, note=note))

    def has_drift(self):
        return bool(self.drifts)


def placeholder(prop):
    """给其余 required 参数构造类型一致的占位值。"""
    enum = prop.get("enum")
    if isinstance(enum, list) and enum:
        return enum[0]
    t = prop.get("type")
    if t == "number" or t == "integer":
        return 1
    if t == "boolean":
        return True
    if t == "array":
        return []
    if t == "object":
        return {}
    return "probe-placeholder"


def wrong_value(prop):
    """类型错误探针值；返回 None 表示该类型不探针。"""
    t = prop.get("type")
    if prop.get("enum"):
        return None  # enum 参数走独立的非法值探针
    if t == "string":
        return 123
    if t in ("number", "integer"):
        return "abc"  # 非数字串（数字字符串合法，见 §3.1）
    if t == "boolean":
        return "abc"
    return None  # array/object 必填参数只参与缺参检查


def error_message(resp):
    """从 JSON-RPC 响应提取人类可读错误消息；返回 (is_error, message, kind)"""
    if not isinstance(resp, dict):
        return (False, "", "invalid")
    if "error" in resp:
        err = resp["error"] or {}
        msg = err.get("message", "")
        extra = err.get("data")
        if isinstance(extra, str) and extra:
            msg = msg + " | data: " + extra
        return (True, msg, "jsonrpc-error(code=%s)" % err.get("code"))
    result = resp.get("result")
    if isinstance(result, dict) and result.get("isError"):
        texts = []
        for c in result.get("content") or []:
            if isinstance(c, dict) and isinstance(c.get("text"), str):
                texts.append(c["text"])
        msg = "\n".join(texts) if texts else json.dumps(result, ensure_ascii=False)
        return (True, msg, "tool-error")
    if "result" in resp:
        return (False, "", "ok")
    return (False, "", "invalid")


def strict_host_check(listing, rep):
    """A+. 严格 MCP 宿主透镜（第六轮新增）。

    按 zcode 等严格校验宿主的 zod 口径核对 tools/list **原始信封**：
    result.tools 必须是数组；每个工具 name/description 非空字符串、
    inputSchema.type=="object"、properties（若在）为对象、required
    **缺席或字符串数组**——`"required":null`（Go nil 切片的序列化形状）
    会让整个服务器被宿主拒收（2026-09-14 真机接入实测：zcode 对一项
    不合即拒收全部工具）。static_check_tool 对非 list required 的静默
    容忍正是本轮漏检原因，本函数不做任何类型折算。
    """
    result = listing.get("result")
    if not isinstance(result, dict):
        rep.drift("<envelope>", "tools", "tools/list result 为对象",
                  "result 缺失或非对象（严格宿主直接拒收）", "-", "-")
        return
    tools = result.get("tools")
    if not isinstance(tools, list):
        rep.drift("<envelope>", "tools", "result.tools 为数组",
                  "实际为 %r（严格宿主直接拒收）" % type(tools).__name__, "-", "-")
        return
    for i, tool in enumerate(tools):
        tag = "tools[%d]" % i
        name = tool.get("name") if isinstance(tool, dict) else None
        label = name if isinstance(name, str) and name else tag
        if not isinstance(tool, dict):
            rep.drift(label, tag, "工具为对象", "实际为 %s" % type(tool).__name__, "-", "-")
            continue
        if not isinstance(name, str) or not name.strip():
            rep.drift(label, tag + ".name", "name 为非空字符串",
                      "实际 %r" % name, "-", "-")
        if not isinstance(tool.get("description"), str):
            rep.drift(label, tag + ".description", "description 为字符串",
                      "实际 %r" % type(tool.get("description")).__name__, "-", "-")
        schema = tool.get("inputSchema")
        if not isinstance(schema, dict):
            rep.drift(label, tag + ".inputSchema", "inputSchema 为对象",
                      "实际为 %s" % type(schema).__name__, "-", "-")
            continue
        if schema.get("type") != "object":
            rep.drift(label, tag + ".inputSchema.type", 'inputSchema.type == "object"',
                      "实际 %r" % schema.get("type"), "-", "-")
        if "required" in schema:
            required = schema["required"]
            if not isinstance(required, list) or not all(isinstance(r, str) for r in required):
                rep.drift(label, tag + ".inputSchema.required",
                          "required 缺席或字符串数组（null 会被严格宿主整服务器拒收）",
                          "实际 %r" % (required,), "-", "-")
        if "properties" in schema and not isinstance(schema["properties"], dict):
            rep.drift(label, tag + ".inputSchema.properties", "properties 为对象",
                      "实际为 %s" % type(schema["properties"]).__name__, "-", "-")


def static_check_tool(tool, rep):
    """A. schema 静态校验；返回 (props, required)。"""
    name = tool.get("name", "<noname>")
    ok = True
    props, required = {}, []

    # 工具名规范
    if not TOOL_NAME_RE.match(name):
        rep.drift(name, "-", "工具名形如 <域>_<动作>（小写+下划线）",
                  "工具名 %r 不符合命名规范" % name, "-", "-")
        ok = False

    # 工具级 description
    if not isinstance(tool.get("description"), str) or not tool["description"].strip():
        rep.drift(name, "-", "工具级 description 非空",
                  "缺失或为空", "-", "-")
        ok = False

    schema = tool.get("inputSchema")
    if not isinstance(schema, dict):
        rep.drift(name, "-", "inputSchema 为 JSON Schema 对象",
                  "缺失或非对象", "-", "-")
        return props, required
    if schema.get("type") != "object":
        rep.drift(name, "-", 'inputSchema.type == "object"',
                  "实际为 %r" % schema.get("type"), "-", "-")
        ok = False

    # 草案外关键字（提示级）
    extra = set(schema.keys()) - KNOWN_SCHEMA_KEYWORDS
    if extra:
        rep.hint(name, "inputSchema 顶层含 JSON Schema 草案之外的关键字: %s（MCP 客户端可能忽略）"
                 % sorted(extra))

    props = schema.get("properties")
    if not isinstance(props, dict):
        props = {}
    required = schema.get("required")
    if not isinstance(required, list):
        required = []

    # required ⊆ properties
    for r in required:
        if r not in props:
            rep.drift(name, r, "required 中的参数必须出现在 properties",
                      "required 含未声明参数 %r" % r, "-", "-")
            ok = False

    for pname, prop in props.items():
        # 参数名 camelCase
        if not PARAM_NAME_RE.match(pname):
            rep.drift(name, pname, "参数名 camelCase（^[a-z][A-Za-z0-9]*$）",
                      "参数名不符合规范", "-", "-")
            ok = False
        if not isinstance(prop, dict):
            rep.drift(name, pname, "参数 schema 为对象", "非对象", "-", "-")
            ok = False
            continue
        # type 存在且合法（允许 type 数组）
        t = prop.get("type")
        tlist = t if isinstance(t, list) else [t]
        if t is None or not tlist or any(x not in VALID_TYPES for x in tlist):
            rep.drift(name, pname, "参数 type 存在且合法（%s）" % sorted(VALID_TYPES),
                      "实际 type=%r" % t, "-", "-")
            ok = False
        # 参数级 description
        if not isinstance(prop.get("description"), str) or not prop["description"].strip():
            rep.drift(name, pname, "参数级 description 非空", "缺失或为空", "-", "-")
            ok = False
        # enum 非空且互异
        enum = prop.get("enum")
        if "enum" in prop:
            if not isinstance(enum, list) or len(enum) == 0:
                rep.drift(name, pname, "enum 非空数组", "实际 %r" % (enum,), "-", "-")
                ok = False
            elif len(set(map(json.dumps, enum))) != len(enum):
                rep.drift(name, pname, "enum 值互异", "存在重复值 %r" % (enum,), "-", "-")
                ok = False
        # 属性级草案外关键字
        pextra = set(prop.keys()) - KNOWN_SCHEMA_KEYWORDS
        if pextra:
            rep.hint(name, "参数 %s 的 schema 含草案外关键字: %s" % (pname, sorted(pextra)))

    return props, required


def build_args(props, required, overrides):
    args = {}
    for r in required:
        if r in props:
            args[r] = placeholder(props[r])
    args.update(overrides)
    return args


def probe_call(sc, tool_name, args, probe_timeout, rep, schema_says, behavior_tag, param):
    """发送 tools/call 探针；返回 (outcome, msg, evidence)。
    outcome ∈ {'error','accepted','timeout','dead'}"""
    status, resp = sc.request("tools/call",
                              {"name": tool_name, "arguments": args}, probe_timeout)
    if status == "timeout":
        rep.not_applicable += 1
        rep.warn(tool_name, param,
                 "探针不适用：调用超过 %ss，疑似触发连接级执行（拨号/桥转发），已跳过" % probe_timeout,
                 request=truncate(args), response="-")
        sc.start()  # 重启重置状态，防迟到响应串线
        return ("timeout", "", None)
    if status == "dead":
        rep.warn(tool_name, param, "sidecar 进程意外退出，该探针未取得响应",
                 request=truncate(args), response="-")
        sc.start()
        return ("dead", "", None)
    is_err, msg, kind = error_message(resp)
    evidence = dict(request=args, response=truncate(resp))
    return (("error" if is_err else "accepted"), msg, evidence)


def behavioral_probes(sc, name, props, required, probe_timeout, rep):
    """B. 行为探针。门禁归一：若该工具全部错误探针返回同一条消息，视为工具级
    统一门禁（工作台门/连接参数门）先于参数校验——参数级校验未到达，探针
    不适用，降级为 WARN，避免把环境门误判为 schema 漂移。"""
    raw = []

    def run(stage, param, args, schema_says):
        outcome, msg, ev = probe_call(sc, name, args, probe_timeout, rep,
                                      schema_says, "-", param)
        raw.append((stage, param, outcome, msg, ev))

    # B1. 空参数 → 应报缺参错误，点名集合与 required 比对
    if required:
        run("B1", "-", {}, "缺参错误应点名 required 参数 %s" % required)

    # B2. required 参数类型错误值探针
    for pname in required:
        prop = props.get(pname, {})
        wv = wrong_value(prop)
        if wv is None:
            continue
        t = prop.get("type")
        args = build_args(props, required, {pname: wv})
        run("B2", pname, args,
            "传类型错误值 %r 应报错点名参数（%s 字段）" % (wv, t))

    # B3. required enum 参数非法值探针
    for pname in required:
        prop = props.get(pname, {})
        enum = prop.get("enum")
        if not isinstance(enum, list) or not enum:
            continue
        args = build_args(props, required, {pname: INVALID_ENUM_PROBE})
        run("B3", pname, args,
            "非法枚举值应报错且列出合法值 %s（§3.3）" % enum)

    err_msgs = [m for (st, p, o, m, ev) in raw if o == "error" and m]
    # 门禁归一：全部错误探针同一条消息，且该消息不点名任何被探参数
    # （点名了参数说明是参数级校验而非工具级门禁，如 "cursorId is required"）
    gate = False
    if len(err_msgs) >= 2 and len(set(err_msgs)) == 1:
        probed = [p for (st, p, o, m, ev) in raw if st in ("B2", "B3") and o == "error"]
        gate = not any(mentions(p, err_msgs[0]) for p in probed)
    stage_label = {"B1": "空参数探针", "B2": "类型错误探针", "B3": "枚举非法值探针"}

    for stage, param, outcome, msg, ev in raw:
        if gate and outcome == "error":
            rep.warn(name, param,
                     "%s被统一门禁消息拦截，参数级校验未到达——探针不适用。门禁消息: %s"
                     % (stage_label[stage], truncate(msg, 160)),
                     request=ev["request"], response=ev["response"])
            continue
        if stage == "B1":
            if outcome == "error":
                named = {p for p in props if mentions(p, msg)}
                missing = [r for r in required if r not in named]
                if missing and CONNECTION_GATE_RE.search(msg):
                    rep.warn(name, "-",
                             "连接参数门先于业务参数校验：空参数错误未点名 required %s（§3.7 校验"
                             "顺序张力，四插件共性）。门禁消息: %s" % (required, truncate(msg, 160)),
                             request=ev["request"], response=ev["response"])
                    continue
                extra = sorted(named - set(required))
                if missing:
                    rep.drift(name, ",".join(missing),
                              "缺参错误应点名全部 required 参数 %s" % required,
                              "错误未点名: %s" % missing,
                              request=ev["request"], response=ev["response"])
                if extra:
                    rep.drift(name, ",".join(extra),
                              "缺参错误只应点名 required 参数 %s" % required,
                              "错误额外点名（多报）: %s" % extra,
                              request=ev["request"], response=ev["response"])
                # 名字不一致：required 中只有 snake_case 变体被点名
                for r in required:
                    if r not in named:
                        continue
                    if r not in msg and to_snake(r) in msg:
                        rep.drift(name, r, "错误消息应点名 schema 参数名 %r" % r,
                                  "消息只出现 snake_case 变体 %r（名字不一致）" % to_snake(r),
                                  request=ev["request"], response=ev["response"])
            elif outcome == "accepted":
                rep.drift(name, "-", "缺参应报错（required=%s）" % required,
                          "空参数被静默接受",
                          request=ev["request"], response=ev["response"])
        elif stage == "B2":
            schema_says = "传类型错误值应报错点名参数"
            if outcome == "error":
                if not mentions(param, msg):
                    if CONNECTION_GATE_RE.search(msg):
                        rep.warn(name, param,
                                 "类型校验被连接解析门短路，未点名该参数（§3.7 要求类型校验先于"
                                 "连接级校验——顺序张力）。门禁消息: %s" % truncate(msg, 160),
                                 request=ev["request"], response=ev["response"])
                    else:
                        rep.drift(name, param, schema_says,
                                  "报错但未点名该参数（消息: %s）" % truncate(msg, 200),
                                  request=ev["request"], response=ev["response"])
            elif outcome == "accepted":
                t = props.get(param, {}).get("type")
                if t == "boolean":
                    rep.warn(name, param,
                             "布尔传 %r 被静默接受——与 §3.2 登记设计一致（files 回退默认/kafka 报错），"
                             "需人工复核未发生语义反转" % ev["request"].get(param),
                             request=ev["request"], response=ev["response"])
                else:
                    rep.drift(name, param, schema_says,
                              "类型错误值被静默接受",
                              request=ev["request"], response=ev["response"])
        elif stage == "B3":
            enum = props.get(param, {}).get("enum") or []
            schema_says = "非法枚举值应报错且列出合法值 %s（§3.3）" % enum
            if outcome == "error":
                listed = [v for v in enum if str(v) in msg]
                if not listed:
                    if CONNECTION_GATE_RE.search(msg):
                        rep.warn(name, param,
                                 "枚举校验被连接解析门短路，未列出合法值 %s（§3.7 顺序张力）。"
                                 "门禁消息: %s" % (enum, truncate(msg, 160)),
                                 request=ev["request"], response=ev["response"])
                    else:
                        rep.drift(name, param, schema_says,
                                  "报错但未列出任何合法枚举值（消息: %s）" % truncate(msg, 200),
                                  request=ev["request"], response=ev["response"])
                elif len(listed) < len(enum):
                    rep.warn(name, param, schema_says + "——错误只列出部分合法值 %s" % listed,
                             request=ev["request"], response=ev["response"])
            elif outcome == "accepted":
                rep.warn(name, param, schema_says +
                         "——非法值被静默接受；若为 §3.3「未知值按缺省降级」的登记设计，"
                         "需确认已在插件 MCP 文档标注",
                         request=ev["request"], response=ev["response"])

    # B4. 未知参数 → 只验证不崩（§3.5）
    args = {UNKNOWN_PARAM_PROBE: True}
    status, resp = sc.request("tools/call",
                              {"name": name, "arguments": args}, probe_timeout)
    if status == "timeout":
        rep.not_applicable += 1
        rep.warn(name, UNKNOWN_PARAM_PROBE,
                 "探针不适用：未知参数调用超过 %ss，疑似触发连接级执行，已跳过" % probe_timeout)
        sc.start()
    elif status == "dead":
        rep.warn(name, UNKNOWN_PARAM_PROBE, "sidecar 进程意外退出")
        sc.start()
    elif error_message(resp)[0] or "result" in resp:
        pass  # 任意合法 JSON-RPC 响应即通过（不崩）
    else:
        rep.hint(name, "未知参数探针收到非常规响应: %s" % truncate(resp))


def run(binary, plugin_hint, probe_timeout, debug, only):
    sc = Sidecar(binary, debug=debug)
    rep = Report()

    status, init = sc.request("initialize", {
        "protocolVersion": "2024-11-05",
        "capabilities": {},
        "clientInfo": {"name": "mcp-schema-check", "version": "1.0.0"},
    }, 20)
    if status != "ok":
        print("FATAL: initialize 失败（status=%s）" % status)
        return 2
    server_info = (init.get("result") or {}).get("serverInfo") or {}
    plugin = server_info.get("name", plugin_hint or "<unknown>")
    print("=== %s ===" % plugin)
    print("binary: %s (mtime: %s)" % (
        binary,
        time.strftime("%Y-%m-%d %H:%M:%S", time.localtime(os.path.getmtime(binary)))))
    if plugin_hint and plugin != plugin_hint:
        rep.hint("-", "serverInfo.name=%r 与 --plugin=%r 不一致" % (plugin, plugin_hint))

    sc.notify("notifications/initialized")
    status, listing = sc.request("tools/list", {}, 20)
    if status != "ok":
        print("FATAL: tools/list 失败（status=%s）" % status)
        return 2
    tools = (listing.get("result") or {}).get("tools") or []
    # 分页兜底（一般不会触发）
    cursor = (listing.get("result") or {}).get("nextCursor")
    guard = 0
    while cursor and guard < 20:
        guard += 1
        status, page = sc.request("tools/list", {"cursor": cursor}, 20)
        if status != "ok":
            break
        r = page.get("result") or {}
        tools.extend(r.get("tools") or [])
        cursor = r.get("nextCursor")

    rep.tool_count = len(tools)
    print("tools: %d" % len(tools))
    # A+. 严格宿主透镜：对 tools/list 原始信封按 zod 口径核对（required:null
    # 等形状问题在这里抓，不再依赖真机才暴露）。
    strict_host_check(listing, rep)
    for tool in tools:
        name = tool.get("name", "<noname>")
        if only and name != only:
            continue
        props, required = static_check_tool(tool, rep)
        behavioral_probes(sc, name, props, required, probe_timeout, rep)
        tool_drifts = [d for d in rep.drifts if d["tool"] == name]
        if not tool_drifts:
            rep.pass_tools += 1
            print("  PASS  %s" % name)
        else:
            print("  DRIFT %s (%d)" % (name, len(tool_drifts)))

    print()
    print("PASS tools: %d / %d" % (rep.pass_tools, len(tools)))
    print("DRIFTS: %d" % len(rep.drifts))
    for i, d in enumerate(rep.drifts, 1):
        print("  [D%d] tool=%s param=%s" % (i, d["tool"], d["param"]))
        print("      schema 说: %s" % d["schema"])
        print("      行为: %s" % d["behavior"])
        if d.get("request") is not None:
            print("      证据 request: %s" % truncate(d["request"], 300))
        if d.get("response"):
            print("      证据 response: %s" % d["response"])
    print("WARNINGS: %d" % len(rep.warns))
    for i, w in enumerate(rep.warns, 1):
        print("  [W%d] tool=%s param=%s: %s" % (i, w["tool"], w["param"], w["note"]))
        if w.get("request") is not None:
            print("      证据 request: %s" % truncate(w["request"], 300))
    print("HINTS: %d" % len(rep.hints))
    for i, h in enumerate(rep.hints, 1):
        print("  [H%d] tool=%s: %s" % (i, h["tool"], h["note"]))
    print("probe-not-applicable: %d" % rep.not_applicable)
    print("RESULT: %s" % ("DRIFT" if rep.has_drift() else "CLEAN"))

    sc.stop()
    return 1 if rep.has_drift() else 0


def main():
    ap = argparse.ArgumentParser(description="DBX 插件 MCP schema↔行为一致性核对器")
    ap.add_argument("--binary", required=True, help="sidecar 二进制路径")
    ap.add_argument("--plugin", default=None, help="预期插件 id（io.dbx.xxx），用于与 serverInfo.name 比对")
    ap.add_argument("--probe-timeout", type=float, default=5.0,
                    help="单个探针超时秒数，超过记 probe-not-applicable（默认 5）")
    ap.add_argument("--only", default=None, help="只核对指定工具（调试用）")
    ap.add_argument("--debug", action="store_true", help="透传 sidecar stderr")
    args = ap.parse_args()
    sys.exit(run(args.binary, args.plugin, args.probe_timeout, args.debug, args.only))


if __name__ == "__main__":
    main()

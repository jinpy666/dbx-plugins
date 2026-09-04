/**
 * Visual fixture host bridge for the Kafka workbench (mock.html).
 *
 * Mirrors the ldap mockDbxHost pattern (which mirrors the real bridge shape):
 * in-memory cluster + `window.dbxPlugin` implementation so the workbench runs
 * in a plain browser (pnpm dev → /mock.html). URL params:
 *   ?theme=light|dark    appearance scheme (default dark)
 *   ?locale=zh-CN|en|…   workbench locale (default zh-CN)
 *   ?err=1               kafka/* domain calls always reject (error fixture)
 *   ?noconn=1            host context has no connectionId (init error fixture)
 *   ?ro=1                readOnly connection（写操作先发 denied kafka/audit 事件
 *                        再抛错，镜像后端 policy.go 分支）
 *   ?nodelete=1          allowDelete=false（删除类额外拒绝）
 */
import "./style.css";

const eventListeners = new Set<(event: DbxPluginEvent) => void>();
const appearanceListeners = new Set<(appearance: DbxPluginAppearance) => void>();
const contextListeners = new Set<(context: Record<string, unknown>) => void>();

const params = new URLSearchParams(location.search);
const readOnly = params.get("ro") === "1";
const allowDelete = params.get("nodelete") !== "1";
const injectError = params.get("err") === "1";

const context: Record<string, unknown> = {
  connectionId: params.get("noconn") === "1" ? "" : "visual-connection",
  workbenchId: "visual-workbench",
  restored: false,
  connection: {
    name: "Demo Kafka",
    host: "dbx-kafka-test",
    port: 9092,
    username: "kafka-app",
    color: "#e11d48",
    readOnly,
    allowDelete,
  },
};

const light = params.get("theme") === "light";
// 与 DBX globals.css 的 :root（pearl 浅色）和 .dark 规范块保持一致。
const appearance: DbxPluginAppearance = {
  colorScheme: light ? "light" : "dark",
  colors: light
    ? { background: "rgb(255 255 255)", foreground: "rgb(10 10 10)", muted: "rgb(245 245 245)", mutedForeground: "rgb(115 115 115)", accent: "rgb(245 245 245)", accentForeground: "rgb(23 23 23)", border: "rgb(229 229 229)", destructive: "rgb(231 0 11)" }
    : { background: "rgb(19 20 22)", foreground: "rgb(215 215 219)", muted: "rgb(42 42 45)", mutedForeground: "rgb(151 152 157)", accent: "rgb(46 47 51)", accentForeground: "rgb(221 221 226)", border: "rgb(110 110 114 / 0.28)", destructive: "rgb(243 98 95)" },
  terminal: { fontFamily: "Cascadia Mono, Consolas, monospace", fontSize: 13 },
};

// 镜像宿主 1.1 theme 通道形状（colors 反查 --color-* 令牌），与真实宿主一致。
const theme: DbxPluginTheme = {
  appearance: appearance.colorScheme,
  tokens: Object.fromEntries(
    Object.entries(appearance.colors).map(([key, value]) => [`--color-${key.replace(/([A-Z])/g, (c) => `-${c.toLowerCase()}`)}`, value]),
  ),
};

// -- in-memory cluster ----------------------------------------------------------

interface MockMessage {
  topic: string;
  partition: number;
  offset: number;
  timestamp: number;
  leaderEpoch?: number;
  key?: string;
  keyBase64?: string;
  valueText?: string;
  valueBase64?: string;
  headers?: Record<string, string>;
  committed?: boolean;
}

interface MockTopic {
  name: string;
  topicId: string;
  isInternal: boolean;
  partitionCount: number;
  replicationFactor: number;
  /** 分区 → 消息数组（offset = 数组下标）。 */
  partitions: MockMessage[][];
}

function utf8ToBase64(text: string): string {
  return btoa(String.fromCharCode(...new TextEncoder().encode(text)));
}

function makeMessage(topic: string, partition: number, offset: number, key: string, value: string, headers: Record<string, string> = {}): MockMessage {
  return {
    topic,
    partition,
    offset,
    timestamp: Date.now(),
    leaderEpoch: 0,
    key,
    keyBase64: utf8ToBase64(key),
    valueText: value,
    valueBase64: utf8ToBase64(value),
    headers,
  };
}

const brokers: KafkaBrokerFixture[] = [
  { nodeId: 1, host: "dbx-kafka-test", port: 9092, rack: "rack-a" },
  { nodeId: 2, host: "dbx-kafka-test-2", port: 9092, rack: "rack-b" },
];

interface KafkaBrokerFixture {
  nodeId: number;
  host: string;
  port: number;
  rack?: string;
}

const brokerConfigs: ConfigEntryFixture[] = [
  { name: "num.partitions", value: "1", source: "DEFAULT_CONFIG", sensitive: false, isDefault: true },
  { name: "log.retention.ms", value: "604800000", source: "DYNAMIC_BROKER_CONFIG", sensitive: false, isDefault: false },
  { name: "sasl.jaas.config", value: "hidden", source: "STATIC_BROKER_CONFIG", sensitive: true, isDefault: false },
];

interface ConfigEntryFixture {
  name: string;
  value: string;
  source?: string;
  sensitive?: boolean;
  isDefault?: boolean;
}

const topics = new Map<string, MockTopic>();

function putTopic(topic: MockTopic) {
  topics.set(topic.name, topic);
}

function seedTopic(name: string, partitionCount: number, isInternal: boolean, samples: Array<{ key: string; value: string; headers?: Record<string, string> }>) {
  const topic: MockTopic = {
    name,
    topicId: `id-${name}`,
    isInternal,
    partitionCount,
    replicationFactor: 1,
    partitions: Array.from({ length: partitionCount }, () => [] as MockMessage[]),
  };
  samples.forEach((sample, index) => {
    const partition = index % partitionCount;
    const message = makeMessage(name, partition, topic.partitions[partition].length, sample.key, sample.value, sample.headers);
    topic.partitions[partition].push(message);
  });
  putTopic(topic);
}

seedTopic("order-events", 2, false, [
  { key: "o-1", value: '{"orderId":"A-1001","amount":42}', headers: { "trace-id": "t-1" } },
  { key: "o-2", value: '{"orderId":"A-1002","amount":7}' },
  { key: "o-3", value: '{"orderId":"A-1003","amount":129}' },
]);
seedTopic("user-signup", 1, false, [
  { key: "u-1", value: '{"userId":"u1","name":"Ada"}' },
  { key: "u-2", value: "binary\u0001payload" },
]);
seedTopic("payment-gateway", 1, false, [{ key: "p-1", value: "5" }]);
seedTopic("connect-offsets", 1, true, [{ key: "c", value: "state" }]);
seedTopic("_schemas", 1, true, [{ key: "s-1", value: '{"schema":"x"}' }]);

interface MockGroup {
  group: string;
  state: string;
  protocolType: string;
  coordinator: number;
  members: Array<{ memberId: string; instanceId?: string; clientId?: string; clientHost?: string; assignments?: Record<string, number[]> }>;
  committed: Map<string, Map<number, number>>; // topic → partition → committed offset
}

const groups = new Map<string, MockGroup>();
const orderGroup: MockGroup = {
  group: "billing-consumer",
  state: "Stable",
  protocolType: "consumer",
  coordinator: 1,
  members: [
    { memberId: "member-1", clientId: "billing-1", clientHost: "/10.0.0.4", assignments: { "order-events": [0, 1] } },
  ],
  committed: new Map([
    ["order-events", new Map([[0, 1], [1, 0]])],
  ]),
};
groups.set(orderGroup.group, orderGroup);

const acls: KafkaAclFixture[] = [
  { resourceType: "TOPIC", resourceName: "order-events", patternType: "LITERAL", principal: "User:billing", host: "*", operation: "READ", permission: "ALLOW" },
];

interface KafkaAclFixture {
  resourceType: string;
  resourceName: string;
  patternType?: string;
  principal: string;
  host?: string;
  operation: string;
  permission: string;
}

// -- stream sessions（fixture 级 ring buffer + 定时发事件）-------------------------

interface StreamSession {
  id: string;
  topic: string;
  buffer: MockMessage[];
  produced: number;
  paused: boolean;
  timer?: number;
}

const streams = new Map<string, StreamSession>();
let streamSeq = 0;

function startStream(topicName: string): StreamSession {
  streamSeq += 1;
  const session: StreamSession = { id: `stream-${streamSeq}`, topic: topicName, buffer: [], produced: 0, paused: false };
  session.timer = window.setInterval(() => {
    if (session.paused) return;
    injectError ? failStream(session) : tickStream(session);
  }, 500);
  streams.set(session.id, session);
  return session;
}

let tickCount = 0;
function tickStream(session: StreamSession) {
  tickCount += 1;
  const batch: MockMessage[] = [];
  for (let index = 0; index < 3; index += 1) {
    session.produced += 1;
    batch.push(makeMessage(session.topic, 0, session.produced, `live-${session.produced}`, `{"tick":${tickCount},"n":${session.produced}}`));
  }
  session.buffer.push(...batch);
  if (session.buffer.length > 10000) session.buffer.splice(0, session.buffer.length - 10000);
  emitEvent("kafka/stream/messages", {
    sessionId: session.id,
    messages: batch,
    totalScanned: session.produced,
    totalMatched: session.produced,
    paused: session.paused,
    bufferSize: session.buffer.length,
  });
}

function failStream(session: StreamSession) {
  emitEvent("kafka/stream/error", { sessionId: session.id, error: "connection lost (fixture error injection)" });
}

function stopStream(sessionId?: string) {
  for (const [id, session] of streams) {
    if (!sessionId || sessionId === id || sessionId === "all") {
      if (session.timer) window.clearInterval(session.timer);
      streams.delete(id);
    }
  }
}

// -- audit / policy ---------------------------------------------------------------

function emitEvent(method: string, eventParams: Record<string, unknown>) {
  for (const listener of eventListeners) listener({ method, params: eventParams } as DbxPluginEvent);
}

// readOnly / allowDelete 下写操作被策略拒绝：先发 denied audit 事件（App.vue
// 横幅与 audit.jsonl 的 denied 语义对应），再抛业务错误（与后端 policy.go 分支
// 行为一致：AuditRecord{Action:"write-policy", Result:"denied"}）。
function denyWrite(action: string, target: string, reason: string): never {
  emitEvent("kafka/audit", {
    connectionId: context.connectionId,
    action,
    target,
    result: "denied",
    detail: reason,
  });
  throw new Error(reason);
}

function guardWrite(action: string, target: string, options: { critical?: boolean; confirmTopic?: string } = {}) {
  if (readOnly) denyWrite(action, target, "connection is read-only (fixture)");
  if (options.critical && !allowDelete) denyWrite(action, target, "allow_delete=false rejects delete operations (fixture)");
  if (options.critical && options.confirmTopic !== undefined && options.confirmTopic !== target) {
    denyWrite(action, target, "confirmTopic mismatch (fixture)");
  }
}

function requireTopic(name: string): MockTopic {
  const topic = topics.get(name);
  if (!topic) throw new Error(`unknown topic: ${name} (fixture)`);
  return topic;
}

// -- filter evaluation（fixture 级 contains/prefix/exact/regex）---------------------

function matchValue(candidate: string | undefined, pattern: string, mode: string): boolean {
  const haystack = candidate ?? "";
  switch (mode) {
    case "prefix":
      return haystack.startsWith(pattern);
    case "exact":
      return haystack === pattern;
    case "regex":
      try {
        return new RegExp(pattern).test(haystack);
      } catch {
        return false;
      }
    default:
      return haystack.includes(pattern);
  }
}

function messageMatches(message: MockMessage, input: Record<string, unknown>): boolean {
  const mode = String(input.matchMode ?? "contains");
  const blob = `${message.key ?? ""}${message.valueText ?? ""}${JSON.stringify(message.headers ?? {})}`;
  if (typeof input.filter === "string" && input.filter && !matchValue(blob, input.filter, mode)) return false;
  if (typeof input.keyFilter === "string" && input.keyFilter && !matchValue(message.key, input.keyFilter, mode)) return false;
  if (typeof input.valueFilter === "string" && input.valueFilter && !matchValue(message.valueText, input.valueFilter, mode)) return false;
  if (typeof input.headerFilter === "string" && input.headerFilter) {
    const headerBlob = JSON.stringify(message.headers ?? {});
    if (!matchValue(headerBlob, input.headerFilter, mode)) return false;
  }
  const from = typeof input.timestampFrom === "number" ? input.timestampFrom : null;
  const to = typeof input.timestampTo === "number" ? input.timestampTo : null;
  if (from !== null && message.timestamp < from) return false;
  if (to !== null && message.timestamp > to) return false;
  const offsetFrom = typeof input.offsetFrom === "number" ? input.offsetFrom : null;
  const offsetTo = typeof input.offsetTo === "number" ? input.offsetTo : null;
  if (offsetFrom !== null && message.offset < offsetFrom) return false;
  if (offsetTo !== null && message.offset > offsetTo) return false;
  return true;
}

function scanTopic(topic: MockTopic, input: Record<string, unknown>): { messages: MockMessage[]; scanned: number } {
  const limit = Number(input.limit ?? 100) || 100;
  const wantedPartitions = Array.isArray(input.partitions) ? (input.partitions as number[]) : null;
  const strategy = String(input.offsetStrategy ?? "latest");
  const scannedAll: Array<{ message: MockMessage; index: number; partition: number }> = [];
  let scanned = 0;
  topic.partitions.forEach((partitionMessages, partition) => {
    if (wantedPartitions && !wantedPartitions.includes(partition)) return;
    const partitionOffsets = (input.partitionOffsets ?? {}) as Record<string, number>;
    let startIndex: number;
    if (strategy === "offset" && partitionOffsets[partition] !== undefined) startIndex = partitionOffsets[partition];
    else if (strategy === "earliest") startIndex = 0;
    else if (strategy === "committed") {
      const committed = orderGroup.committed.get(topic.name)?.get(partition);
      startIndex = committed === undefined ? 0 : committed;
    } else if (strategy === "timestamp") {
      const after = typeof input.offsetTime === "number" ? input.offsetTime : Date.parse(String(input.offsetTime ?? "")) || 0;
      startIndex = partitionMessages.findIndex((message) => message.timestamp >= after);
      if (startIndex < 0) startIndex = partitionMessages.length;
    } else startIndex = Math.max(0, partitionMessages.length - limit);
    for (let index = startIndex; index < partitionMessages.length; index += 1) {
      scanned += 1;
      scannedAll.push({ message: partitionMessages[index], index, partition });
    }
  });
  const matched = scannedAll.filter((entry) => messageMatches(entry.message, input)).map((entry) => entry.message);
  return { messages: matched.slice(0, limit), scanned };
}

// -- request / invoke ---------------------------------------------------------------

const request: DbxPluginApi["request"] = async <T = unknown>(method: string) =>
  (method === "host.getContext" ? context : null) as T;

const invoke: DbxPluginApi["invoke"] = async <T = unknown>(method: string, rawParams?: unknown) => {
  const input = (rawParams ?? {}) as Record<string, unknown>;
  let result: unknown = { success: true };
  if (injectError && method !== "kafka/connections/statuses" && method !== "host.getContext") {
    throw new Error("connection lost (fixture error injection)");
  }
  if (method === "kafka/brokers/list") {
    result = { brokers };
  } else if (method === "kafka/brokers/config") {
    result = { entries: brokerConfigs };
  } else if (method === "kafka/topics/list") {
    const includeInternal = input.includeInternal === true;
    const list = [...topics.values()]
      .filter((topic) => includeInternal || !topic.isInternal)
      .map((topic) => ({ name: topic.name, topicId: topic.topicId, isInternal: topic.isInternal, partitionCount: topic.partitionCount, replicationFactor: topic.replicationFactor }));
    result = { topics: list };
  } else if (method === "kafka/topics/describe") {
    const topic = requireTopic(String(input.topic ?? ""));
    result = {
      partitions: topic.partitions.map((partition, index) => ({
        partition: index,
        leader: brokers[index % brokers.length].nodeId,
        leaderEpoch: 0,
        replicas: [brokers[index % brokers.length].nodeId],
        isr: [brokers[index % brokers.length].nodeId],
        offlineReplicas: [],
        isHealthy: true,
      })),
    };
  } else if (method === "kafka/topics/create") {
    const names = (input.topics ?? []) as string[];
    const partitionCount = Number(input.partitions ?? 1);
    const replicationFactor = Number(input.replicationFactor ?? 1);
    const config = (input.config ?? {}) as Record<string, string>;
    guardWrite("topics/create", names.join(","));
    const results = names.map((name) => {
      if (topics.has(name)) return { topic: name, ok: false, error: "topic already exists" };
      seedTopic(name, partitionCount, false, []);
      const seeded = topics.get(name)!;
      seeded.replicationFactor = replicationFactor;
      void config;
      return { topic: name, ok: true };
    });
    result = { results };
  } else if (method === "kafka/topics/delete") {
    const names = (input.topics ?? []) as string[];
    for (const name of names) {
      guardWrite("topics/delete", name, { critical: true, confirmTopic: String(input.confirmTopic ?? "") });
    }
    result = {
      results: names.map((name) => {
        const existed = topics.delete(name);
        return { topic: name, ok: existed, error: existed ? undefined : "unknown topic" };
      }),
    };
  } else if (method === "kafka/topics/partitions/update") {
    const wanted = (input.partitions ?? {}) as Record<string, number>;
    guardWrite("topics/partitions/update", JSON.stringify(wanted));
    const results: Array<{ topic: string; ok: boolean; error?: string }> = [];
    for (const [name, nextCount] of Object.entries(wanted)) {
      const topic = topics.get(name);
      if (!topic) {
        results.push({ topic: name, ok: false, error: "unknown topic" });
        continue;
      }
      if (nextCount <= topic.partitionCount) {
        results.push({ topic: name, ok: false, error: "partition count must be larger" });
        continue;
      }
      while (topic.partitions.length < nextCount) topic.partitions.push([]);
      topic.partitionCount = nextCount;
      results.push({ topic: name, ok: true });
    }
    result = { results };
  } else if (method === "kafka/topics/config/get" || method === "kafka/brokers/config") {
    result = {
      entries: [
        { name: "retention.ms", value: "604800000", source: "DYNAMIC_TOPIC_CONFIG", sensitive: false, isDefault: false },
        { name: "cleanup.policy", value: "delete", source: "DEFAULT_CONFIG", sensitive: false, isDefault: true },
        { name: "password.secret", value: "hidden", source: "DYNAMIC_TOPIC_CONFIG", sensitive: true, isDefault: false },
      ],
    };
  } else if (method === "kafka/topics/config/alter") {
    guardWrite("topics/config/alter", String(input.topic ?? ""));
    result = { entries: brokerConfigs };
  } else if (method === "kafka/topics/offsets/list") {
    const names = (input.topics ?? []) as string[];
    const rows: Array<Record<string, unknown>> = [];
    for (const name of names) {
      const topic = topics.get(name);
      if (!topic) continue;
      topic.partitions.forEach((partition, index) => {
        const last = partition[partition.length - 1];
        rows.push({ topic: name, partition: index, offset: partition.length, timestamp: last?.timestamp, leaderEpoch: 0 });
      });
    }
    result = { rows };
  } else if (method === "kafka/groups/list") {
    result = {
      groups: [...groups.values()].map((group) => ({ group: group.group, state: group.state, protocolType: group.protocolType, coordinator: group.coordinator })),
    };
  } else if (method === "kafka/groups/describe") {
    const group = groups.get(String(input.group ?? ""));
    result = { members: group?.members ?? [] };
  } else if (method === "kafka/groups/offsets/list") {
    const group = groups.get(String(input.group ?? ""));
    const rows: Array<Record<string, unknown>> = [];
    let totalLag = 0;
    if (group) {
      for (const [topicName, partitions] of group.committed) {
        const topic = topics.get(topicName);
        topic?.partitions.forEach((partition, index) => {
          const committedOffset = partitions.get(index);
          const endOffset = partition.length;
          const lag = committedOffset === undefined ? endOffset : Math.max(0, endOffset - committedOffset);
          totalLag += lag;
          rows.push({
            topic: topicName,
            partition: index,
            startOffset: 0,
            endOffset,
            committedOffset: committedOffset ?? null,
            lag,
            hasCommitted: committedOffset !== undefined,
          });
        });
      }
    }
    result = { rows, totalLag };
  } else if (method === "kafka/groups/delete") {
    const group = String(input.group ?? "");
    guardWrite("groups/delete", group, { critical: true });
    result = { success: groups.delete(group) };
  } else if (method === "kafka/groups/offsets/reset") {
    const group = groups.get(String(input.group ?? ""));
    guardWrite("groups/offsets/reset", String(input.group ?? ""));
    const topicsArg = ((input.topics ?? []) as string[]).filter(Boolean);
    const rows: Array<Record<string, unknown>> = [];
    if (group) {
      for (const [topicName, partitions] of group.committed) {
        if (topicsArg.length > 0 && !topicsArg.includes(topicName)) continue;
        const topic = topics.get(topicName);
        topic?.partitions.forEach((partition, index) => {
          const resetTo = String(input.resetTo ?? "earliest");
          let next = 0;
          if (resetTo === "latest") next = partition.length;
          else if (resetTo === "timestamp") next = partition.filter((message) => message.timestamp >= Number(input.timestampMs ?? 0)).length;
          else if (resetTo === "partitionOffset") {
            const wanted = (input.partitionOffsets ?? {}) as Record<string, number>;
            next = wanted[index] ?? 0;
          }
          partitions.set(index, next);
          rows.push({ topic: topicName, partition: index, ok: true });
        });
      }
    }
    result = { rows };
  } else if (method === "kafka/acls/list") {
    const filter_ = (input.filter ?? {}) as Record<string, string>;
    const matched = acls.filter((acl) =>
      Object.entries(filter_).every(([key, value]) => {
        if (!value) return true;
        const candidate = (acl as unknown as Record<string, string>)[key] ?? "";
        return candidate === value || value === "ANY";
      }),
    );
    result = { acls: matched };
  } else if (method === "kafka/acls/create") {
    const acl = (input.acl ?? {}) as KafkaAclFixture;
    guardWrite("acls/create", `${acl.resourceType}:${acl.resourceName}`);
    acls.push(acl);
    result = { success: true };
  } else if (method === "kafka/acls/delete") {
    const filter_ = (input.filter ?? {}) as Record<string, string>;
    guardWrite("acls/delete", JSON.stringify(filter_), { critical: true });
    const before = acls.length;
    for (let index = acls.length - 1; index >= 0; index -= 1) {
      const acl = acls[index] as unknown as Record<string, string>;
      const matches = Object.entries(filter_).every(([key, value]) => !value || acl[key] === value || value === "ANY");
      if (matches) acls.splice(index, 1);
    }
    result = { matched: before - acls.length };
  } else if (method === "kafka/messages/produce") {
    const topicName = String(input.topic ?? "");
    const topic = requireTopic(topicName);
    guardWrite("messages/produce", topicName);
    const count = Math.min(Number(input.count ?? 1) || 1, 1000);
    const partition = typeof input.partition === "number" ? input.partition : 0;
    const key = typeof input.key === "string" ? input.key : "";
    const value = String(input.value ?? "");
    const headers = (input.headers ?? {}) as Record<string, string>;
    let lastOffset = -1;
    for (let index = 0; index < count; index += 1) {
      lastOffset = topic.partitions[partition]?.length ?? 0;
      topic.partitions[partition]?.push(makeMessage(topicName, partition, lastOffset, count > 1 ? `${key}-${index}` : key, value, headers));
    }
    result = { partition, offset: lastOffset, timestamp: Date.now() };
  } else if (method === "kafka/messages/consume") {
    const topic = requireTopic(String(input.topic ?? ""));
    const scan = scanTopic(topic, input);
    result = {
      messages: scan.messages,
      scanned: scan.scanned,
      matched: scan.messages.length,
      limited: scan.messages.length >= Number(input.limit ?? 100),
      hasMore: false,
      nextPartitionOffsets: {},
    };
  } else if (method === "kafka/messages/export") {
    const topic = requireTopic(String(input.topic ?? ""));
    const scan = scanTopic(topic, input);
    const format = String(input.format ?? "json");
    if (format === "csv") {
      const lines = ["topic,partition,offset,timestamp,key,value,headers"];
      for (const message of scan.messages) {
        lines.push([message.topic, String(message.partition), String(message.offset), String(message.timestamp), message.key ?? "", message.valueText ?? "", JSON.stringify(message.headers ?? {})].map(quoteCsv).join(","));
      }
      result = { content: lines.join("\r\n"), filename: "kafka-messages.csv", contentType: "text/csv" };
    } else {
      result = {
        content: JSON.stringify(
          scan.messages.map((message) => ({ topic: message.topic, partition: message.partition, offset: message.offset, timestamp: message.timestamp, key: message.key, value: message.valueText, headers: message.headers ?? {} })),
          null,
          2,
        ),
        filename: "kafka-messages.json",
        contentType: "application/json",
      };
    }
  } else if (method === "kafka/stream/start") {
    const topicName = String(input.topic ?? "");
    requireTopic(topicName);
    if (readOnly && input.commit === true) guardWrite("stream/start", topicName);
    const session = startStream(topicName);
    result = { sessionId: session.id };
  } else if (method === "kafka/stream/stop") {
    stopStream(input.all === true ? "all" : typeof input.sessionId === "string" ? input.sessionId : undefined);
    result = { success: true };
  } else if (method === "kafka/stream/pause" || method === "kafka/stream/resume") {
    const session = streams.get(String(input.sessionId ?? ""));
    if (!session) throw new Error("stream session not found (fixture)");
    session.paused = method === "kafka/stream/pause";
    result = {
      status: { paused: session.paused, totalScanned: session.produced, totalMatched: session.produced, bufferSize: session.buffer.length, partitionOffsets: { "0": session.buffer.length } },
    };
  } else if (method === "kafka/stream/status") {
    const session = streams.get(String(input.sessionId ?? ""));
    if (!session) throw new Error("stream session not found (fixture)");
    result = {
      status: { paused: session.paused, totalScanned: session.produced, totalMatched: session.produced, bufferSize: session.buffer.length, partitionOffsets: { "0": session.buffer.length } },
    };
  } else if (method === "kafka/stream/messages") {
    const session = streams.get(String(input.sessionId ?? ""));
    if (!session) throw new Error("stream session not found (fixture)");
    const offset = Number(input.offset ?? 0) || 0;
    const limit = Number(input.limit ?? 100) || 100;
    result = { messages: session.buffer.slice(offset, offset + limit), total: session.buffer.length };
  } else if (method === "kafka/presets/list") {
    result = { presets: JSON.parse(localStorage.getItem("kafka-mock-presets") ?? "[]") };
  } else if (method === "kafka/presets/save") {
    const presets = JSON.parse(localStorage.getItem("kafka-mock-presets") ?? "[]") as unknown[];
    const incoming = (input.preset ?? {}) as Record<string, unknown>;
    const index = presets.findIndex((preset) => (preset as Record<string, unknown>).id === incoming.id);
    if (index >= 0) presets[index] = incoming;
    else presets.push(incoming);
    localStorage.setItem("kafka-mock-presets", JSON.stringify(presets));
    result = { presets };
  } else if (method === "kafka/presets/remove") {
    const id = String(input.id ?? "");
    const presets = (JSON.parse(localStorage.getItem("kafka-mock-presets") ?? "[]") as Array<Record<string, unknown>>).filter((preset) => preset.id !== id);
    localStorage.setItem("kafka-mock-presets", JSON.stringify(presets));
    result = { presets };
  } else if (method === "kafka/connections/statuses") {
    result = {
      statuses: [{ connectionId: String(context.connectionId), status: "connected", readOnly, allowDelete, lastUsedAt: Date.now() }],
    };
  } else if (method === "kafka/audit") {
    // never emitted by the mock host itself (audit goes through events)
  }
  return result as T;
};

function quoteCsv(value: string): string {
  if (/[",\n\r]/.test(value)) return `"${value.replace(/"/g, '""')}"`;
  return value;
}

// window.dbxPlugin 组装（与宿主 Host API 1.0 面一致；invoke ?? request 双方法）。
window.dbxPlugin = {
  ready: Promise.resolve(context),
  context,
  appearance,
  theme,
  locale: params.get("locale") || "zh-CN",
  request,
  invoke,
  notify: async () => undefined,
  sendBinary: async () => undefined,
  onEvent: (listener) => {
    eventListeners.add(listener);
    return () => eventListeners.delete(listener);
  },
  onBinary: () => () => undefined,
  onAppearanceChange: (listener) => {
    appearanceListeners.add(listener);
    listener(appearance);
    return () => appearanceListeners.delete(listener);
  },
  onLocaleChange: () => () => undefined,
  onContextChange: (listener) => {
    contextListeners.add(listener);
    listener(context);
    return () => contextListeners.delete(listener);
  },
  decodeBase64: (value) => Uint8Array.from(atob(value), (character) => character.charCodeAt(0)),
  encodeBase64: (value) => {
    const bytes = value instanceof Uint8Array ? value : new Uint8Array(value as ArrayBuffer);
    let binary = "";
    for (const byte of bytes) binary += String.fromCharCode(byte);
    return btoa(binary);
  },
  workbenchState: { set: async () => undefined },
  clipboard: { readText: async () => "", writeText: async () => undefined },
};

export { context, appearance };

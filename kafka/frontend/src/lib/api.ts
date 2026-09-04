/**
 * Sidecar call wrapper for the Kafka workbench.
 *
 * All `kafka/*` domain methods take only `connectionId` (the workbench never
 * receives credentials — the host drives `connection/test|connect|disconnect`
 * itself). The current connection id comes from the host context delivered
 * over `window.dbxPlugin`. Errors surface through `showError(cause)`.
 * 方法契约：IMPL_PLAN_DBX_KAFKA §5（新方法必须同步 PROTOCOL_KAFKA 文档）。
 */

export type OffsetStrategy = "latest" | "earliest" | "committed" | "timestamp" | "offset";
export type IsolationLevel = "read_uncommitted" | "read_committed";
export type MatchMode = "contains" | "prefix" | "exact" | "regex";
export type DecodeMode = "none" | "base64";
export type Decompression = "none" | "gzip" | "lz4" | "zstd" | "snappy";
export type Compression = "none" | "gzip" | "lz4" | "zstd" | "snappy";

export interface KafkaMessage {
  topic: string;
  partition: number;
  offset: number;
  /** unix ms（sidecar JSON number）。 */
  timestamp: number;
  leaderEpoch?: number;
  /** UTF-8 安全预览（非法字节已替换），可能与原始字节不一致。 */
  key?: string;
  /** 恒完整的 key base64（valueBase64 同理保真，见 §5.3 二进制保真约定）。 */
  keyBase64?: string;
  valueText?: string;
  valueBase64?: string;
  headers?: Record<string, string>;
  committed?: boolean;
  decodeError?: string;
  truncated?: boolean;
  /** SR 解码挂载信息（consume/stream 附带；produce 挂载后回填）。 */
  schemaId?: number;
  schemaSubject?: string;
  schemaVersion?: number;
}

/** Schema Registry 解码挂载（consume/stream/produce 共用；version 省略 = latest）。 */
export interface SchemaAttach {
  subject: string;
  version?: number;
  format: "avro" | "json";
}

export interface FieldFilter {
  source: "value" | "key" | "header" | "topic" | "partition" | "offset" | "timestamp";
  path?: string;
  operator: "contains" | "prefix" | "exact" | "regex" | "exists" | "not_exists" | "gt" | "gte" | "lt" | "lte";
  value: string;
  /** 前端行开关；上线载荷中剥离（仅启用的行会携带）。 */
  enabled?: boolean;
}

export interface ConsumeParams {
  topic: string;
  groupId?: string;
  offsetStrategy: OffsetStrategy;
  offsetTime?: string | number;
  partitions?: number[];
  partitionOffsets?: Record<string, number>;
  limit?: number;
  timeoutMs?: number;
  maxScanRecords?: number;
  isolationLevel?: IsolationLevel;
  commit?: boolean;
  filter?: string;
  keyFilter?: string;
  valueFilter?: string;
  headerFilter?: string;
  matchMode?: MatchMode;
  fieldFilters?: FieldFilter[];
  timestampFrom?: number;
  timestampTo?: number;
  offsetFrom?: number;
  offsetTo?: number;
  decode?: DecodeMode;
  decompression?: Decompression;
  /** SR 解码挂载（Phase 2；与 decode 内层解码可叠加，后端先 SR 再内层）。 */
  schema?: SchemaAttach;
}

export interface ConsumeResult {
  messages: KafkaMessage[];
  scanned: number;
  matched: number;
  limited: boolean;
  hasMore: boolean;
  nextPartitionOffsets?: Record<string, number>;
}

export interface KafkaBroker {
  nodeId: number;
  host: string;
  port: number;
  rack?: string;
}

/** `kafka/brokers/list` 顶层返回；ZK 模式下带 `connectionSource=zookeeper`。 */
export interface BrokersListResult {
  brokers: KafkaBroker[];
  connectionSource?: "kafka" | "zookeeper" | string;
}

export interface ConfigEntry {
  name: string;
  value: string;
  source?: string;
  sensitive?: boolean;
  isDefault?: boolean;
}

export interface KafkaTopic {
  name: string;
  topicId?: string;
  isInternal?: boolean;
  partitionCount: number;
  replicationFactor: number;
  error?: string;
}

export interface TopicPartitionInfo {
  partition: number;
  leader: number;
  leaderEpoch?: number;
  replicas: number[];
  isr: number[];
  offlineReplicas: number[];
  isHealthy?: boolean;
}

export interface TopicOffsetRow {
  topic: string;
  partition: number;
  offset: number;
  timestamp?: number;
  leaderEpoch?: number;
}

export interface KafkaGroup {
  group: string;
  state?: string;
  protocolType?: string;
  coordinator?: number | string;
}

export interface GroupMember {
  memberId: string;
  instanceId?: string;
  clientId?: string;
  clientHost?: string;
  assignments?: Record<string, number[]>;
}

export interface GroupOffsetRow {
  topic: string;
  partition: number;
  startOffset?: number;
  endOffset?: number;
  committedOffset?: number | null;
  lag?: number;
  hasCommitted?: boolean;
}

export interface KafkaAcl {
  resourceType: string;
  resourceName: string;
  patternType?: string;
  principal: string;
  host?: string;
  operation: string;
  permission: string;
}

export interface AclFilter {
  resourceType?: string;
  resourceName?: string;
  patternType?: string;
  principal?: string;
  host?: string;
  operation?: string;
  permission?: string;
}

export interface ProduceResult {
  partition: number;
  offset: number;
  timestamp?: number;
}

export interface StreamStatus {
  paused?: boolean;
  totalScanned?: number;
  totalMatched?: number;
  bufferSize?: number;
  partitionOffsets?: Record<string, number>;
}

export interface KafkaPreset {
  id: string;
  name: string;
  /**
   * 消费预设：序列化的 ConsumeParams（不含 topic，应用时回填当前选中 topic）。
   * 监控预设：附加 `type:"monitor"` 与 `monitor` 载荷（Phase 2，同一 store 复用）。
   */
  params: ConsumeParams & {
    type?: "consume" | "monitor";
    monitor?: MonitorPresetParams;
  };
}

/** MonitorPanel 监控方案（走 kafka/presets/*，type=monitor）。 */
export interface MonitorPresetParams {
  group: string;
  topics: string[];
  intervalSec: number;
  threshold: number;
}

export interface KafkaConnectionStatus {
  connectionId: string;
  /** 后端 KafkaConnectionStatus.Status（契约三态：connected | idle | error）。 */
  status: "connected" | "idle" | "error";
  /** 策略层只读门禁（表单 read_only ∥ 宿主 read_only），旧 sidecar 可能缺省。 */
  readOnly?: boolean;
  /** 删除类操作门禁（allow_delete 表单；read_only 下后端强制无效）。旧 sidecar 可能缺省。 */
  allowDelete?: boolean;
  lastError?: string;
  /** unix 毫秒时间戳（sidecar JSON number）。 */
  lastUsedAt?: number;
}

/** Current connection id, injected by App.vue once the host context resolves. */
let currentConnectionId = "";

export function setKafkaConnectionId(connectionId: string) {
  currentConnectionId = String(connectionId || "");
}

export function getKafkaConnectionId(): string {
  return currentConnectionId;
}

function requireConnectionId(): string {
  if (!currentConnectionId) {
    throw new Error("Kafka connection context is not ready (missing connectionId)");
  }
  return currentConnectionId;
}

// params 放宽为 object（接口类型无 index signature，调用面更贴合方法契约），
// 组装载荷时收窄为记录展开。
async function callKafka<T>(method: string, params: object = {}, options?: { timeoutMs?: number }): Promise<T> {
  const api = window.dbxPlugin;
  if (!api) throw new Error("DBX Host API unavailable");
  const invoke = (api.invoke ?? api.request).bind(api);
  return invoke<T>(method, { connectionId: requireConnectionId(), ...(params as Record<string, unknown>) }, options);
}

// -- domain methods (§5.2 of IMPL_PLAN_DBX_KAFKA) -----------------------------

export const kafkaApi = {
  // brokers
  brokersList() {
    return callKafka<{ brokers: KafkaBroker[] }>("kafka/brokers/list");
  },
  brokersConfig(brokerId: number) {
    return callKafka<{ entries: ConfigEntry[] }>("kafka/brokers/config", { brokerId });
  },

  // topics
  topicsList(includeInternal = false) {
    return callKafka<{ topics: KafkaTopic[] }>("kafka/topics/list", includeInternal ? { includeInternal: true } : {});
  },
  topicsDescribe(topic: string) {
    return callKafka<{ partitions: TopicPartitionInfo[] }>("kafka/topics/describe", { topic });
  },
  topicsCreate(topics: string[], partitions: number, replicationFactor: number, config?: Record<string, string>) {
    return callKafka<{ results: Array<{ topic: string; ok: boolean; error?: string }> }>(
      "kafka/topics/create",
      { topics, partitions, replicationFactor, ...(config && Object.keys(config).length > 0 ? { config } : {}) },
    );
  },
  topicsDelete(topics: string[], confirmTopic: string) {
    return callKafka<{ results: Array<{ topic: string; ok: boolean; error?: string }> }>(
      "kafka/topics/delete",
      { topics, confirmTopic },
    );
  },
  topicsPartitionsUpdate(partitions: Record<string, number>) {
    return callKafka<{ results: Array<{ topic: string; ok: boolean; error?: string }> }>(
      "kafka/topics/partitions/update",
      { partitions },
    );
  },
  topicsConfigGet(topic: string) {
    return callKafka<{ entries: ConfigEntry[] }>("kafka/topics/config/get", { topic });
  },
  topicsConfigAlter(topic: string, config: Record<string, string>, deleteKeys: string[] = []) {
    return callKafka<{ entries: ConfigEntry[] }>("kafka/topics/config/alter", { topic, config, deleteKeys });
  },
  topicsOffsetsList(topics: string[], offsetTime?: string | number) {
    return callKafka<{ rows: TopicOffsetRow[] }>(
      "kafka/topics/offsets/list",
      offsetTime !== undefined ? { topics, offsetTime } : { topics },
    );
  },

  // groups
  groupsList() {
    return callKafka<{ groups: KafkaGroup[] }>("kafka/groups/list");
  },
  groupsDescribe(group: string) {
    return callKafka<{ members: GroupMember[] }>("kafka/groups/describe", { group });
  },
  groupsOffsetsList(group: string, topics?: string[]) {
    return callKafka<{ rows: GroupOffsetRow[]; totalLag?: number }>(
      "kafka/groups/offsets/list",
      topics?.length ? { group, topics } : { group },
    );
  },
  groupsDelete(group: string) {
    return callKafka<{ success: boolean }>("kafka/groups/delete", { group });
  },
  groupsOffsetsReset(
    group: string,
    topics: string[],
    resetTo: "earliest" | "latest" | "timestamp" | "partitionOffset",
    extra: { timestampMs?: number; partitionOffsets?: Record<string, number> } = {},
  ) {
    return callKafka<{ rows: Array<{ topic: string; partition: number; ok: boolean; error?: string }> }>(
      "kafka/groups/offsets/reset",
      { group, topics, resetTo, ...extra },
    );
  },

  // acls
  aclsList(filter: AclFilter) {
    return callKafka<{ acls: KafkaAcl[] }>("kafka/acls/list", { filter });
  },
  aclsCreate(acl: KafkaAcl) {
    return callKafka<{ success: boolean }>("kafka/acls/create", { acl });
  },
  aclsDelete(filter: AclFilter) {
    return callKafka<{ matched: number }>("kafka/acls/delete", { filter });
  },

  // messages
  messagesProduce(params: {
    topic: string;
    key?: string;
    value: string;
    headers?: Record<string, string>;
    partition?: number;
    count?: number;
    compression?: Compression;
  }) {
    return callKafka<ProduceResult>("kafka/messages/produce", params);
  },
  messagesConsume(params: ConsumeParams, options?: { timeoutMs?: number }) {
    return callKafka<ConsumeResult>("kafka/messages/consume", params, options);
  },
  messagesExport(params: ConsumeParams & { format: "json" | "csv"; limit: number }) {
    return callKafka<{ content: string; filename: string; contentType: string }>("kafka/messages/export", params);
  },

  // stream
  streamStart(params: ConsumeParams) {
    return callKafka<{ sessionId: string }>("kafka/stream/start", params);
  },
  streamStop(sessionId?: string, all = false) {
    return callKafka<{ success: boolean }>("kafka/stream/stop", sessionId && !all ? { sessionId } : { all: true });
  },
  streamPause(sessionId: string) {
    return callKafka<{ status: StreamStatus }>("kafka/stream/pause", { sessionId });
  },
  streamResume(sessionId: string) {
    return callKafka<{ status: StreamStatus }>("kafka/stream/resume", { sessionId });
  },
  streamStatus(sessionId: string) {
    return callKafka<{ status: StreamStatus }>("kafka/stream/status", { sessionId });
  },
  streamMessages(sessionId: string, offset: number, limit: number) {
    return callKafka<{ messages: KafkaMessage[]; total?: number }>("kafka/stream/messages", { sessionId, offset, limit });
  },

  // presets + statuses（照 ldap/presets、ldap/connections/statuses 形态）
  presetsList() {
    return callKafka<{ presets: KafkaPreset[] }>("kafka/presets/list");
  },
  presetsSave(preset: KafkaPreset) {
    return callKafka<{ presets: KafkaPreset[] }>("kafka/presets/save", { preset });
  },
  presetsRemove(id: string) {
    return callKafka<{ presets: KafkaPreset[] }>("kafka/presets/remove", { id });
  },
  connectionStatuses() {
    return callKafka<{ statuses: KafkaConnectionStatus[] }>("kafka/connections/statuses");
  },
};

// -- events (§5.4) ------------------------------------------------------------

export interface KafkaStreamMessagesEvent {
  sessionId: string;
  messages: KafkaMessage[];
  totalScanned?: number;
  totalMatched?: number;
  paused?: boolean;
  /** 当前 sidecar ring buffer 内的消息数（前端 dropped 估算用）。 */
  bufferSize?: number;
}

export interface KafkaStreamErrorEvent {
  sessionId: string;
  error: string;
}

export interface KafkaAuditEvent {
  connectionId: string;
  action: string;
  target: string;
  result: "ok" | "denied" | "error";
}

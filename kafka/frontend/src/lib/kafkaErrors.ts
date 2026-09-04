/**
 * Friendly Kafka error mapping. Sidecar errors surface raw franz-go / policy
 * strings that users cannot act on. `friendlyKafkaError` maps the common
 * failure classes (SASL/TLS auth, unreachable brokers, policy gates,
 * timeouts) to localized, actionable text and passes anything unknown through
 * unchanged. 写门禁拒绝（read-only / allow-delete / confirmTopic）优先识别，
 * 与后端 policy.go 的错误码语义（blocked）对应。
 */
import { t } from "./i18n";

const RULES: ReadonlyArray<{ pattern: RegExp; key: string }> = [
  // 门禁类（policy.go）：先于网络类匹配
  { pattern: /confirm[_ ]?topic/i, key: "err.confirmTopic" },
  { pattern: /allow.?delete|delete.*(dis|not)\s*allowed/i, key: "err.deleteBlocked" },
  { pattern: /read.?only|blocked by policy/i, key: "err.readOnly" },
  // 认证/TLS 类
  { pattern: /sasl|authentication|scram|badcredentials|result code 49/i, key: "err.auth" },
  { pattern: /certificate|x509|unknown authority|tls.*handshake|ssl/i, key: "err.tls" },
  // 集群/资源类
  { pattern: /unknown topic|topic.*not.*(found|exist)|result code 3/i, key: "err.topicNotFound" },
  { pattern: /unknown (topic )?partition|out of range|offset.*(out of range|not available)/i, key: "err.partition" },
  { pattern: /group.*(not.*(found|exist)|empty)|unknown member|illegal generation/i, key: "err.group" },
  { pattern: /record too large|message size.*larger|bytes.*exceed/i, key: "err.messageTooLarge" },
  { pattern: /not leader|leader.*not.*available|coordinator.*not.*available|rebalance/i, key: "err.meta" },
  // 网络/超时类（网络先于通用超时）
  {
    pattern: /network error|connection refused|no such host|connection reset|broken pipe|i\/o timeout|eof|dial/i,
    key: "err.network",
  },
  { pattern: /timeout|timed out|deadline exceeded/i, key: "err.timeout" },
];

export const friendlyKafkaError = (message: string): string => {
  const raw = String(message ?? "");
  for (const rule of RULES) {
    if (rule.pattern.test(raw)) return t(rule.key);
  }
  return raw;
};

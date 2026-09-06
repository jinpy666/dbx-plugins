// friendlyKafkaError 规则映射测试：门禁类优先于网络类，未知错误原样透传。
import { describe, expect, it } from "vitest";
import { friendlyKafkaError } from "./kafkaErrors";

describe("friendlyKafkaError", () => {
  it("maps policy gates before network errors", () => {
    expect(friendlyKafkaError("produce blocked: connection is read-only")).not.toContain("read-only (raw)");
    expect(friendlyKafkaError("connection is read-only")).toBe(friendlyKafkaError("connection is read-only"));
    expect(friendlyKafkaError("allow_delete=false rejects kafka/topics/delete")).not.toBe("allow_delete=false rejects kafka/topics/delete");
    expect(friendlyKafkaError("confirmTopic mismatch")).not.toBe("confirmTopic mismatch");
  });

  it("maps auth, tls and network classes", () => {
    expect(friendlyKafkaError("SASL authentication failed: scram credential mismatch")).not.toMatch(/SASL authentication failed/);
    expect(friendlyKafkaError("x509: certificate signed by unknown authority")).not.toBe("x509: certificate signed by unknown authority");
    expect(friendlyKafkaError("dial tcp 10.0.0.1:9092: connect: connection refused")).not.toBe("dial tcp 10.0.0.1:9092: connect: connection refused");
    expect(friendlyKafkaError("context deadline exceeded")).not.toBe("context deadline exceeded");
  });

  it("passes unknown messages through unchanged", () => {
    const raw = "totally unknown broker quirk";
    expect(friendlyKafkaError(raw)).toBe(raw);
  });

  // Phase 3 F2（§12.7）：MSK/OAUTHBEARER 失败类（无凭据/region 缺失）先于通用
  // SASL 认证类给出针对性可行动文案。
  it("maps msk/oauth failure classes with actionable text (Phase 3 F2)", () => {
    const credential = friendlyKafkaError("msk iam: unable to load AWS credentials (no IMDS)");
    expect(credential).not.toContain("unable to load AWS credentials");
    expect(credential).toBe(friendlyKafkaError("OAUTHBEARER token source msk_iam: no credential found"));
    const region = friendlyKafkaError("msk_region is required for MSK IAM");
    expect(region).not.toBe("msk_region is required for MSK IAM");
    expect(region).toBe(friendlyKafkaError("region missing for aws msk"));
    // region 类优先命中（错误串同时含 region 与 credential 字样时不落错类）。
    expect(region).toBe(friendlyKafkaError("msk_region is required"));
    // 普通 SASL 认证失败不被 msk 规则误吞。
    expect(friendlyKafkaError("SASL authentication failed")).not.toBe(credential);
  });
});

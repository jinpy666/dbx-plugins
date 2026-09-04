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
});

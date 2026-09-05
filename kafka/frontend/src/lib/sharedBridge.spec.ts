// shared/frontend 公共适配层薄 spec（README 约定：每个插件保留一条引用断言，
// 证明该插件工具链下 import 解析、打包、行为都成立）。
// 本插件事件为 JSON 载荷、无二进制通道，但仍按规范统一消费桥 binary 双形状。
import { describe, expect, it } from "vitest";
import { bridgeBinaryBytes } from "../../../../shared/frontend/binaryEvent";

describe("shared bridge binary event normalization (thin plugin check)", () => {
  it("prefers zero-copy bytes and falls back to base64", () => {
    const bytes = new Uint8Array([1, 2, 3]);
    expect(bridgeBinaryBytes({ channel: "kafka", data: bytes }, (value) => new Uint8Array([9]))).toBe(bytes);
    const decoded = bridgeBinaryBytes(
      { channel: "kafka", dataBase64: "AQID" },
      (value) => Uint8Array.from(atob(value), (character) => character.charCodeAt(0)),
    );
    expect([...decoded]).toEqual([1, 2, 3]);
    expect(bridgeBinaryBytes({ channel: "kafka" }, () => new Uint8Array()).length).toBe(0);
  });
});

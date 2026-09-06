// Unit tests for the binary value helper library (M6-N3): magic-number
// sniffing on hand-built byte arrays (no real image fixtures), chunk-safe
// base64 round-trips (>64 KB), hex view formatting, PEM re-wrapping, the
// 5 MB guard and the attribute-name heuristic.
import { describe, expect, it } from "vitest";
import {
  MAX_BINARY_BYTES,
  assertBinarySize,
  base64ToBytes,
  bytesToBase64,
  looksBinaryAttribute,
  prettyPem,
  sniffBinaryKind,
  toHexView,
} from "./binaryValue";

const textBytes = (text: string): Uint8Array => new TextEncoder().encode(text);

// Deterministic pseudo-random fill so large round-trips are reproducible.
function fillBytes(length: number): Uint8Array {
  const bytes = new Uint8Array(length);
  for (let i = 0; i < length; i += 1) bytes[i] = (i * 31 + (i >> 8)) % 256;
  return bytes;
}

describe("sniffBinaryKind", () => {
  it("detects jpeg by the FF D8 FF magic", () => {
    expect(sniffBinaryKind(new Uint8Array([0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10]))).toBe("jpeg");
  });

  it("detects png by the 89 50 4E 47 magic", () => {
    expect(sniffBinaryKind(new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]))).toBe("png");
  });

  it("detects gif by the GIF8 magic", () => {
    expect(sniffBinaryKind(textBytes("GIF89a"))).toBe("gif");
  });

  it("detects webp via RIFF container + WEBP tag", () => {
    const bytes = textBytes("RIFF");
    const webp = new Uint8Array(12);
    webp.set(bytes, 0);
    webp.set(textBytes("WEBP"), 8);
    expect(sniffBinaryKind(webp)).toBe("webp");
    // RIFF alone (WAV/AVI) is not webp.
    expect(sniffBinaryKind(textBytes("RIFFxxxx"))).toBe("unknown");
  });

  it("detects pem when the decoded bytes are PEM text", () => {
    const pem = "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----";
    expect(sniffBinaryKind(textBytes(pem))).toBe("pem");
  });

  it("returns unknown for arbitrary bytes and empty input", () => {
    expect(sniffBinaryKind(new Uint8Array([0x00, 0x01, 0x02]))).toBe("unknown");
    expect(sniffBinaryKind(new Uint8Array())).toBe("unknown");
    // Short prefixes must not produce false positives.
    expect(sniffBinaryKind(new Uint8Array([0xff, 0xd8]))).toBe("unknown");
  });
});

describe("base64 round-trips", () => {
  it("round-trips small payloads via bytesToBase64/base64ToBytes", () => {
    const bytes = new Uint8Array([0x00, 0xff, 0x10, 0x20, 0x7f, 0x80]);
    const encoded = bytesToBase64(bytes);
    expect(encoded).toBe(btoa(String.fromCharCode(...bytes)));
    expect(base64ToBytes(encoded)).toEqual(bytes);
  });

  it("round-trips a >64 KB payload without hitting call stack limits", () => {
    const bytes = fillBytes(200_000);
    expect(base64ToBytes(bytesToBase64(bytes))).toEqual(bytes);
  });

  it("round-trips payloads straddling the 32 KB chunk boundary", () => {
    for (const length of [0x8000 - 1, 0x8000, 0x8000 + 1]) {
      const bytes = fillBytes(length);
      expect(base64ToBytes(bytesToBase64(bytes))).toEqual(bytes);
    }
  });

  it("tolerates whitespace (PEM line wraps) and rejects malformed input", () => {
    expect(base64ToBytes("aGVs\nbG8g\nd29y\nbGQh")).toEqual(textBytes("hello world!"));
    expect(() => base64ToBytes("abc")).toThrow(/invalid base64/);
    expect(() => base64ToBytes("ab=c")).toThrow(/invalid base64/);
    expect(() => base64ToBytes("a@bc=")).toThrow(/invalid base64/);
  });

  it("decodes empty input to empty bytes", () => {
    expect(base64ToBytes("")).toHaveLength(0);
    expect(base64ToBytes("  \n ")).toHaveLength(0);
  });
});

describe("toHexView", () => {
  const pngMagic = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52]);

  it("renders offset + hex + ASCII columns in hexdump -C style", () => {
    expect(toHexView(pngMagic)).toBe("00000000  89 50 4e 47 0d 0a 1a 0a  00 00 00 0d 49 48 44 52 |.PNG........IHDR|");
  });

  it("pads short trailing lines and keeps offsets monotonic", () => {
    const view = toHexView(new Uint8Array([0x41, 0x42, 0x43, 0x44, 0x45]), 4);
    const lines = view.split("\n");
    expect(lines).toHaveLength(2);
    expect(lines[0]).toBe(`00000000  ${"41 42 43 44".padEnd(23)} |ABCD|`);
    expect(lines[1].startsWith("00000004  45")).toBe(true);
    expect(lines[1].endsWith("|E   |")).toBe(true);
  });

  it("accepts base64 input directly", () => {
    expect(toHexView(bytesToBase64(pngMagic))).toBe(toHexView(pngMagic));
  });

  it("renders empty input as an empty string", () => {
    expect(toHexView(new Uint8Array())).toBe("");
  });
});

describe("prettyPem", () => {
  it("wraps bare base64 at 64 columns inside BEGIN/END CERTIFICATE", () => {
    expect(prettyPem("aGVsbG8gd29ybGQh")).toBe("-----BEGIN CERTIFICATE-----\naGVsbG8gd29ybGQh\n-----END CERTIFICATE-----");
  });

  it("re-wraps an already-PEM value and keeps its original label", () => {
    const body = "aGVsbG8gd29ybGQhIHRoaXMgaXMgYSBsb25nZXIgY2VydCBib2R5ISEh";
    const sloppy = `-----BEGIN X509 CERTIFICATE-----\n${body.slice(0, 10)}\n${body.slice(10)}\n-----END X509 CERTIFICATE-----`;
    const pretty = prettyPem(sloppy);
    const lines = pretty.split("\n");
    expect(lines[0]).toBe("-----BEGIN X509 CERTIFICATE-----");
    expect(lines[lines.length - 1]).toBe("-----END X509 CERTIFICATE-----");
    expect(lines.slice(1, -1).join("")).toBe(body);
    for (const line of lines.slice(1, -1)) expect(line.length).toBeLessThanOrEqual(64);
  });

  it("honours an explicit label for bare base64 input", () => {
    const pretty = prettyPem("aGVsbG8gd29ybGQh", "PKCS12");
    expect(pretty.startsWith("-----BEGIN PKCS12-----")).toBe(true);
    expect(pretty.endsWith("-----END PKCS12-----")).toBe(true);
  });

  it("throws on values that are not valid base64", () => {
    expect(() => prettyPem("not base64!!")).toThrow(/invalid base64/);
    expect(() => prettyPem("")).toThrow(/invalid base64/);
  });
});

describe("assertBinarySize", () => {
  it("accepts payloads at or below the 5 MB guard", () => {
    expect(() => assertBinarySize(new Uint8Array(MAX_BINARY_BYTES))).not.toThrow();
  });

  it("throws above the 5 MB guard", () => {
    expect(() => assertBinarySize(new Uint8Array(MAX_BINARY_BYTES + 1))).toThrow(/byte limit/);
  });
});

describe("looksBinaryAttribute", () => {
  it("matches photo-like and certificate-like names", () => {
    for (const name of ["jpegPhoto", "photo", "thumbnailPhoto", "userCertificate", "userSMIMECertificate", "caCertificate", "userPKCS12"]) {
      expect(looksBinaryAttribute(name)).toBe(true);
    }
  });

  it("strips the ;binary transfer suffix before matching", () => {
    expect(looksBinaryAttribute("userCertificate;binary")).toBe(true);
    expect(looksBinaryAttribute(" JPEGPHOTO ")).toBe(true);
  });

  it("rejects textual attributes", () => {
    for (const name of ["cn", "mail", "description", "objectClass", ""]) {
      expect(looksBinaryAttribute(name)).toBe(false);
    }
  });
});

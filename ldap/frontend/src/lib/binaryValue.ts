// Binary attribute value helpers (M6-N3): magic-number sniffing, chunk-safe
// base64 round-trips, hex viewer text and PEM pretty-printing. Pure functions
// only — the EntryEditorDialog integration is a separate step; upload reuses
// the existing entry/modify channel (FileReader → base64), so no new protocol
// methods are introduced here.

export type BinaryKind = "jpeg" | "png" | "gif" | "webp" | "pem" | "unknown";

/** Front-end upload guard: keeps stdio-jsonl messages small (5 MB per value). */
export const MAX_BINARY_BYTES = 5 * 1024 * 1024;

/** Longest base64 line in a normalized PEM block (RFC 7468). */
const PEM_WRAP_WIDTH = 64;

const BASE64_PATTERN = /^[A-Za-z0-9+/]+={0,2}$/;

const PEM_HEADER_PREFIX = "-----BEGIN";

const PEM_LABEL_PATTERN = /^-----BEGIN ([A-Z0-9][A-Z0-9 /-]*)-----/;

function hasPrefix(bytes: Uint8Array, magic: readonly number[]): boolean {
  if (bytes.length < magic.length) return false;
  return magic.every((byte, index) => bytes[index] === byte);
}

function asciiSlice(bytes: Uint8Array, start: number, end: number): string {
  let text = "";
  for (let i = start; i < end && i < bytes.length; i += 1) text += String.fromCharCode(bytes[i] as number);
  return text;
}

/**
 * Detect well-known binary payloads by magic number. PEM covers values whose
 * decoded bytes are already ASCII PEM text ("-----BEGIN …"); bare base64 DER
 * certificates have no magic and stay "unknown" (callers may still offer a PEM
 * view based on the attribute name heuristic, see looksBinaryAttribute).
 */
export function sniffBinaryKind(bytes: Uint8Array): BinaryKind {
  if (hasPrefix(bytes, [0xff, 0xd8, 0xff])) return "jpeg";
  if (hasPrefix(bytes, [0x89, 0x50, 0x4e, 0x47])) return "png";
  if (hasPrefix(bytes, [0x47, 0x49, 0x46, 0x38])) return "gif";
  if (bytes.length >= 12 && asciiSlice(bytes, 0, 4) === "RIFF" && asciiSlice(bytes, 8, 12) === "WEBP") return "webp";
  if (asciiSlice(bytes, 0, PEM_HEADER_PREFIX.length).trimStart().startsWith(PEM_HEADER_PREFIX)) return "pem";
  return "unknown";
}

/**
 * Decode base64 into bytes. Whitespace (including PEM line wraps) is ignored;
 * anything else that is not valid base64 throws so callers can surface an
 * error instead of rendering garbage.
 */
export function base64ToBytes(value: string): Uint8Array {
  const normalized = value.replace(/\s+/g, "");
  if (!normalized) return new Uint8Array(0);
  if (normalized.length % 4 !== 0 || !BASE64_PATTERN.test(normalized)) {
    throw new Error("invalid base64 value");
  }
  const binary = atob(normalized);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
  return bytes;
}

/**
 * Encode bytes into base64 without blowing the call stack on large inputs:
 * String.fromCharCode is applied to 32 KB slices instead of the whole array
 * (a >64 KB spread of arguments throws "too many arguments" in engines).
 */
export function bytesToBase64(bytes: Uint8Array): string {
  const CHUNK = 0x8000;
  let binary = "";
  for (let offset = 0; offset < bytes.length; offset += CHUNK) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + CHUNK));
  }
  return btoa(binary);
}

/**
 * Classic offset + hex + ASCII three-column dump (hexdump -C style):
 * "00000000  89 50 4e 47 0d 0a 1a 0a  00 00 00 0d 49 48 44 52  |.PNG........IHDR|".
 * Accepts base64 directly for convenience. Empty input renders as "".
 */
export function toHexView(bytes: string | Uint8Array, bytesPerLine = 16): string {
  const data = typeof bytes === "string" ? base64ToBytes(bytes) : bytes;
  const lines: string[] = [];
  const groupWidth = 8 * 3 - 1;
  for (let offset = 0; offset < data.length; offset += bytesPerLine) {
    const row = data.subarray(offset, offset + bytesPerLine);
    const hexParts = Array.from(row, (byte) => byte.toString(16).padStart(2, "0"));
    const groups: string[] = [];
    for (let i = 0; i < hexParts.length; i += 8) {
      groups.push(hexParts.slice(i, i + 8).join(" ").padEnd(groupWidth, " "));
    }
    const ascii = Array.from(row, (byte) => (byte >= 0x20 && byte < 0x7f ? String.fromCharCode(byte) : "."))
      .join("")
      .padEnd(bytesPerLine, " ");
    lines.push(`${offset.toString(16).padStart(8, "0")}  ${groups.join("  ")} |${ascii}|`);
  }
  return lines.join("\n");
}

/**
 * Render a certificate-ish value as a normalized PEM block: bare base64 is
 * wrapped at 64 columns inside BEGIN/END delimiters; already-PEM text keeps
 * its original label but is re-wrapped and re-padded. Invalid base64 throws.
 */
export function prettyPem(value: string, label = "CERTIFICATE"): string {
  const trimmed = value.trim();
  const beginMatch = PEM_LABEL_PATTERN.exec(trimmed);
  const effectiveLabel = beginMatch?.[1] ?? label;
  const bodySource = beginMatch ? trimmed.replace(/-----(?:BEGIN|END) [A-Z0-9][A-Z0-9 /-]*-----/g, "") : trimmed;
  const body = bodySource.replace(/\s+/g, "");
  if (!body || body.length % 4 !== 0 || !BASE64_PATTERN.test(body)) {
    throw new Error("invalid base64 value");
  }
  const wrapped = (body.match(new RegExp(`.{1,${PEM_WRAP_WIDTH}}`, "g")) ?? []).join("\n");
  return `-----BEGIN ${effectiveLabel}-----\n${wrapped}\n-----END ${effectiveLabel}-----`;
}

/** Throws when a value would blow the per-value upload guard. */
export function assertBinarySize(bytes: Uint8Array): void {
  if (bytes.byteLength > MAX_BINARY_BYTES) {
    throw new Error(`binary value exceeds the ${MAX_BINARY_BYTES} byte limit`);
  }
}

const BINARY_ATTRIBUTE_NAMES = new Set(["userpkcs12"]);

/**
 * Attribute-name heuristic for binary syntax when schema data is unavailable
 * (schema cache down / syntax OID unknown): jpegPhoto, photo, thumbnailPhoto,
 * *Certificate (userCertificate, userSMIMECertificate, …) and userPKCS12.
 * Accepts the ";binary" transfer suffix ("userCertificate;binary"). Exported
 * so the EntryEditorDialog integration can reuse the same judgement.
 */
export function looksBinaryAttribute(name: string): boolean {
  const bare = name.trim().toLowerCase().split(";")[0] ?? "";
  if (!bare) return false;
  if (BINARY_ATTRIBUTE_NAMES.has(bare)) return true;
  return bare.endsWith("photo") || bare.endsWith("certificate");
}

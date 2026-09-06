/**
 * RFC 2307 `userPassword` hash helpers (M6 N2).
 *
 * Pure frontend counterpart of PLA `Attribute/Password/*`, narrowed to the
 * schemes Web Crypto covers natively: SHA / SSHA / SHA256 / SSHA256 / SHA512 /
 * SSHA512 + CLEARTEXT. MD5 and the crypt family ({MD5}, {CRYPT}, {SMD5},
 * argon2/bcrypt …) are deliberately NOT supported (project decision: no
 * catch-up with PHP-specific schemes) and are excluded at the type level.
 *
 * Value shape: `{SCHEME}base64` where salted schemes append a random 16-byte
 * salt to the digest before base64 (OpenLDAP RFC 2307 style). Plain SHA
 * schemes are unsalted. Values still travel through entry/modify unchanged —
 * the sidecar stays untouched.
 *
 * `hashPassword` is async because Web Crypto `subtle.digest` is the only
 * portable digest primitive available in the browser workbench.
 */

export type PasswordScheme =
    | "{SSHA}"
    | "{SSHA256}"
    | "{SSHA512}"
    | "{SHA}"
    | "{SHA256}"
    | "{SHA512}"
    | "{CLEARTEXT}";

interface SchemeMeta {
    /** Web Crypto algorithm name understood by `crypto.subtle.digest`. */
    algorithm: "SHA-1" | "SHA-256" | "SHA-512";
    /** Salted schemes append `SALT_BYTES` random bytes to the digest. */
    salted: boolean;
}

// Canonical (uppercase) scheme labels, in dropdown display order.
export const PASSWORD_SCHEMES: readonly PasswordScheme[] = Object.freeze([
    "{SSHA}",
    "{SSHA256}",
    "{SSHA512}",
    "{SHA}",
    "{SHA256}",
    "{SHA512}",
    "{CLEARTEXT}",
]);

const SCHEME_META: Readonly<Record<Exclude<PasswordScheme, "{CLEARTEXT}">, SchemeMeta>> = Object.freeze({
    "{SSHA}": { algorithm: "SHA-1", salted: true },
    "{SSHA256}": { algorithm: "SHA-256", salted: true },
    "{SSHA512}": { algorithm: "SHA-512", salted: true },
    "{SHA}": { algorithm: "SHA-1", salted: false },
    "{SHA256}": { algorithm: "SHA-256", salted: false },
    "{SHA512}": { algorithm: "SHA-512", salted: false },
});

const SALT_BYTES = 16;

export interface ParsedPasswordHash {
    scheme: PasswordScheme;
    /** Raw payload after the `{SCHEME}` prefix (base64 digest(+salt)); omitted for prefix-less plaintext. */
    params?: string;
}

// Loose base64 shape check: the payload is opaque to us, we only guard against
// values that are obviously not a hash blob before trusting the scheme label.
const BASE64_RE = /^[A-Za-z0-9+/]+={0,2}$/;

const bytesToBase64 = (bytes: Uint8Array): string => {
    let binary = "";
    for (const byte of bytes) binary += String.fromCharCode(byte);
    return btoa(binary);
};

const base64ToBytes = (value: string): Uint8Array => {
    const binary = atob(value);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
    return bytes;
};

/**
 * Hash `plain` into the RFC 2307 storage string for `scheme`.
 * Salted schemes draw a fresh 16-byte salt via `crypto.getRandomValues`;
 * `{CLEARTEXT}` returns the input unchanged (still an explicit choice).
 */
export const hashPassword = async (plain: string, scheme: PasswordScheme): Promise<string> => {
    const value = String(plain ?? "");
    if (scheme === "{CLEARTEXT}") return value;
    const meta = SCHEME_META[scheme];
    if (!meta) return value; // unreachable for the closed PasswordScheme union
    const data = new TextEncoder().encode(value);
    // RFC 2307 salted schemes digest `plain ‖ salt` (salt also travels in the
    // stored blob tail); unsalted schemes digest the plaintext only.
    const salt = meta.salted ? crypto.getRandomValues(new Uint8Array(SALT_BYTES)) : null;
    const input = salt ? new Uint8Array([...data, ...salt]) : data;
    const digest = new Uint8Array(await crypto.subtle.digest(meta.algorithm, input));
    const payload = salt ? bytesToBase64(new Uint8Array([...digest, ...salt])) : bytesToBase64(digest);
    return `${scheme}${payload}`;
};

/**
 * Identify the scheme of an existing `userPassword` value.
 * Recognises every supported `{SCHEME}` prefix (case-insensitive) and treats
 * prefix-less non-empty values as plaintext (`{CLEARTEXT}`). Unknown scheme
 * labels ({MD5}, {CRYPT} …) return null so callers can warn instead of
 * silently mis-hashing.
 */
export const parsePasswordHash = (value: string): ParsedPasswordHash | null => {
    const source = String(value ?? "").trim();
    if (!source) return null;
    const match = /^\{([^{}]*)\}([\s\S]*)$/.exec(source);
    if (!match) return { scheme: "{CLEARTEXT}" };
    const label = `{${match[1].toUpperCase()}}` as PasswordScheme;
    const params = match[2] ?? "";
    if (label === "{CLEARTEXT}") return { scheme: "{CLEARTEXT}", params };
    if (!SCHEME_META[label]) return null;
    if (params && !BASE64_RE.test(params)) return null;
    return { scheme: label, params };
};

// Unambiguous alphabet for generated passwords: no 0/O/1/l/I look-alikes and
// no symbols (keeps LDIF/stdio-jsonl transport and manual transcription safe).
const PASSWORD_ALPHABET = "abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";
const ALPHABET_MAX = Math.floor(256 / PASSWORD_ALPHABET.length) * PASSWORD_ALPHABET.length;

const DEFAULT_PASSWORD_LENGTH = 16;
const MAX_PASSWORD_LENGTH = 128;

/**
 * Random password from the unambiguous alphabet. Uses rejection sampling on
 * `crypto.getRandomValues` bytes so every character stays uniformly likely
 * (no modulo bias). Invalid lengths fall back to the 16-char default.
 */
export const generateRandomPassword = (length = DEFAULT_PASSWORD_LENGTH): string => {
    const requested = Math.floor(Number(length));
    const size = Number.isFinite(requested) && requested > 0 ? Math.min(MAX_PASSWORD_LENGTH, requested) : DEFAULT_PASSWORD_LENGTH;
    const chars: string[] = [];
    while (chars.length < size) {
        const bytes = crypto.getRandomValues(new Uint8Array(size - chars.length));
        for (const byte of bytes) {
            if (byte < ALPHABET_MAX) chars.push(PASSWORD_ALPHABET[byte % PASSWORD_ALPHABET.length]);
        }
    }
    return chars.join("");
};

/** Re-derive the digest for a stored hash and compare with `plain` (client-side sanity check only; authoritative verification is a bind against the server). */
export const verifyPasswordHash = async (plain: string, stored: string): Promise<boolean> => {
    const parsed = parsePasswordHash(stored);
    if (!parsed || parsed.scheme === "{CLEARTEXT}" || !parsed.params) return false;
    const meta = SCHEME_META[parsed.scheme];
    const payload = base64ToBytes(parsed.params);
    if (meta.salted) {
        if (payload.length <= SALT_BYTES) return false;
        const split = payload.length - SALT_BYTES;
        const digest = new Uint8Array(await crypto.subtle.digest(meta.algorithm, new Uint8Array([...new TextEncoder().encode(String(plain ?? "")), ...payload.slice(split)])));
        const expected = payload.slice(0, split);
        return digest.length === expected.length && digest.every((byte, index) => byte === expected[index]);
    }
    const digest = new Uint8Array(await crypto.subtle.digest(meta.algorithm, new TextEncoder().encode(String(plain ?? ""))));
    return digest.length === payload.length && digest.every((byte, index) => byte === payload[index]);
};

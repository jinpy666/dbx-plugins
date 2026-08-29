/**
 * RFC 2849 compatible LDIF serializer/parser for LDAP entries.
 *
 * Ported from tiny-rdm `frontend/src/modules/tools/ldap/utils/ldif.js`.
 * Handles LDIF for *entry* import/export only — change records
 * (changetype: add/modify/delete/modrdn) are intentionally NOT supported
 * (see RFC 2849 §2).
 */

const DEFAULT_LINE_WIDTH = 76
const DEFAULT_VERSION = 1
const CONTINUATION_PREFIX = ' '

export interface LdifEntry {
    dn: string;
    attributes: Record<string, string[]>;
}

export interface LdifParseError {
    line: number;
    message: string;
}

export interface LdifParseResult {
    entries: LdifEntry[];
    errors: LdifParseError[];
}

export interface LdifSerializeOptions {
    version?: number;
    lineWidth?: number;
    includeVersion?: boolean;
}

/**
 * Determine whether an LDIF attribute value needs base64 encoding.
 * Per RFC 2849 §4 (SAFE-STRING): a value is safe if it does not start
 * with NUL/LF/CR/SPACE/COLON/LESS-THAN and contains no NUL/LF/CR, and
 * every byte is in the printable ASCII range.
 */
function needsBase64(value: string): boolean {
    if (value === '') return false
    const first = value.charCodeAt(0)
    // Leading SPACE, COLON, LESS-THAN, or NUL → unsafe-init-char
    if (first === 0x20 || first === 0x3a || first === 0x3c || first === 0x00) return true
    // Trailing SPACE — RFC 2849 says safe-string must not end with SPACE
    if (value.charCodeAt(value.length - 1) === 0x20) return true
    for (let i = 0; i < value.length; i++) {
        const code = value.charCodeAt(i)
        // Control chars except TAB are unsafe; >= 0x7f is non-ASCII → base64
        if (code === 0x00 || code === 0x0a || code === 0x0d) return true
        if (code < 0x20 && code !== 0x09) return true
        if (code > 0x7e) return true
    }
    return false
}

/**
 * UTF-8 safe base64 encoder for browser environments.
 * `btoa` only accepts Latin-1, so we round-trip via percent-encoding.
 */
function encodeBase64Utf8(str: string): string {
    // encodeURIComponent → UTF-8 %xx escapes → unescape → Latin-1 bytes → btoa
    return btoa(unescape(encodeURIComponent(str)))
}

/** UTF-8 safe base64 decoder. Inverse of encodeBase64Utf8. */
function decodeBase64Utf8(b64: string): string {
    return decodeURIComponent(escape(atob(b64)))
}

/**
 * Fold a single logical LDIF line into physical lines no wider than
 * `lineWidth`. Continuation lines start with a single SPACE per RFC 2849 §8.
 */
function foldLine(line: string, lineWidth: number): string {
    if (line.length <= lineWidth) return line
    const parts: string[] = []
    parts.push(line.slice(0, lineWidth))
    let offset = lineWidth
    // Each continuation line has 1 leading space, so it can carry (lineWidth - 1) chars
    const contentPerLine = Math.max(1, lineWidth - 1)
    while (offset < line.length) {
        parts.push(CONTINUATION_PREFIX + line.slice(offset, offset + contentPerLine))
        offset += contentPerLine
    }
    return parts.join('\n')
}

function serializeAttributeLine(name: string, value: string, lineWidth: number): string {
    if (needsBase64(value)) {
        return foldLine(`${name}:: ${encodeBase64Utf8(value)}`, lineWidth)
    }
    return foldLine(`${name}: ${value}`, lineWidth)
}

function serializeDnLine(dn: string, lineWidth: number): string {
    if (needsBase64(dn)) {
        return foldLine(`dn:: ${encodeBase64Utf8(dn)}`, lineWidth)
    }
    return foldLine(`dn: ${dn}`, lineWidth)
}

/** Serialize a list of LDAP entries into an LDIF document. */
export function serializeEntriesToLdif(entries: LdifEntry[], options: LdifSerializeOptions = {}): string {
    const lineWidth = options.lineWidth ?? DEFAULT_LINE_WIDTH
    const includeVersion = options.includeVersion ?? true
    const version = options.version ?? DEFAULT_VERSION

    const lines: string[] = []
    if (includeVersion) lines.push(`version: ${version}`)

    const list = Array.isArray(entries) ? entries : []
    list.forEach((entry, idx) => {
        if (!entry || typeof entry.dn !== 'string') return
        if (idx > 0 || includeVersion) lines.push('')
        lines.push(serializeDnLine(entry.dn, lineWidth))
        const attrs = entry.attributes || {}
        for (const name of Object.keys(attrs)) {
            const values = attrs[name]
            if (!Array.isArray(values)) continue
            for (const value of values) {
                lines.push(serializeAttributeLine(name, String(value), lineWidth))
            }
        }
    })

    return lines.join('\n')
}

interface UnfoldedLine {
    line: string;
    startLine: number;
    isBlank: boolean;
    isComment: boolean;
}

/**
 * Unfold a raw LDIF text into logical lines. Continuation lines (starting
 * with SPACE) per RFC 2849 §8 are joined to the previous logical line;
 * comments and blank lines are passed through as markers so the caller can
 * detect entry boundaries.
 */
function unfoldLines(text: string): UnfoldedLine[] {
    const physical = text.split(/\r\n|\r|\n/)
    const out: UnfoldedLine[] = []
    let current: UnfoldedLine | null = null
    for (let i = 0; i < physical.length; i++) {
        const raw = physical[i]
        if (raw.length > 0 && raw.charCodeAt(0) === 0x20) {
            if (current && !current.isBlank) {
                // Comments are logical lines too (RFC 2849 §8): folded comment
                // continuation lines join the comment instead of becoming bogus
                // attribute lines.
                current.line += current.isComment ? raw : raw.slice(1)
                continue
            }
            // Continuation with no anchor — treat as its own logical line
        }
        if (current) out.push(current)
        if (raw === '') {
            current = { line: '', startLine: i + 1, isBlank: true, isComment: false }
        } else if (raw.charCodeAt(0) === 0x23) {
            current = { line: raw, startLine: i + 1, isBlank: false, isComment: true }
        } else {
            current = { line: raw, startLine: i + 1, isBlank: false, isComment: false }
        }
    }
    if (current) out.push(current)
    return out
}

/**
 * Parse a single `name: value` or `name:: base64` LDIF line.
 * Returns null if the line is malformed.
 */
function parseAttributeLine(line: string): { name: string; value: string } | null {
    const colonIdx = line.indexOf(':')
    if (colonIdx <= 0) return null
    const name = line.slice(0, colonIdx)
    let rest = line.slice(colonIdx + 1)
    let isBase64 = false
    if (rest.startsWith(':')) {
        isBase64 = true
        rest = rest.slice(1)
    }
    // RFC 2849: a single SPACE follows the colon(s); strip leading whitespace defensively
    const value = rest.replace(/^[ \t]+/, '')
    if (isBase64) {
        try {
            return { name, value: decodeBase64Utf8(value) }
        } catch (e) {
            // Re-throw with a sentinel so caller can attach a line number
            const err = new Error(`base64 decode failed: ${(e as Error).message || e}`) as Error & { code?: string }
            err.code = 'BASE64_DECODE'
            throw err
        }
    }
    return { name, value }
}

/**
 * Parse an LDIF text into entries plus a per-entry error list.
 * Errors are best-effort: a single broken entry does not abort the whole parse.
 */
export function parseLdif(text: string): LdifParseResult {
    const result: LdifParseResult = { entries: [], errors: [] }
    if (typeof text !== 'string' || text.length === 0) return result

    const logical = unfoldLines(text)
    let entry: LdifEntry | null = null
    let entryHasError = false

    const flush = () => {
        if (entry && !entryHasError) result.entries.push(entry)
        entry = null
        entryHasError = false
    }

    for (const item of logical) {
        if (item.isComment) continue
        if (item.isBlank) {
            flush()
            continue
        }
        const trimmed = item.line
        // Version header is informational; RFC 2849 only places it before the
        // first dn. Inside an entry "version" is an ordinary attribute and must
        // not be swallowed.
        if (entry === null && /^version\s*:/i.test(trimmed)) {
            continue
        }
        let parsed: { name: string; value: string } | null
        try {
            parsed = parseAttributeLine(trimmed)
        } catch (e) {
            result.errors.push({ line: item.startLine, message: (e as Error).message })
            entryHasError = true
            continue
        }
        if (!parsed) {
            result.errors.push({ line: item.startLine, message: `malformed line: ${trimmed}` })
            entryHasError = true
            continue
        }
        const lowerName = parsed.name.toLowerCase()
        if (lowerName === 'changetype') {
            result.errors.push({
                line: item.startLine,
                message: 'changetype records are not supported; only entry records can be parsed',
            })
            entryHasError = true
            continue
        }
        if (lowerName === 'dn') {
            if (entry) flush()
            entry = { dn: parsed.value, attributes: {} }
            continue
        }
        if (!entry) {
            result.errors.push({
                line: item.startLine,
                message: `attribute ${parsed.name} appeared before dn`,
            })
            entryHasError = true
            continue
        }
        if (!entry.attributes[parsed.name]) entry.attributes[parsed.name] = []
        entry.attributes[parsed.name].push(parsed.value)
    }
    flush()
    return result
}

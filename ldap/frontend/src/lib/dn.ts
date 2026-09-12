/**
 * DN parsing helpers. Ported from tiny-rdm
 * `frontend/src/modules/tools/ldap/utils/dn.js` — escape-aware splitting of
 * DNs / RDNs (handles `\,` escapes, quoted values and multi-valued `+` RDNs).
 */

const trimToString = (value: unknown): string => {
    const text = String(value ?? '').trimStart();
    let end = text.length;
    // An escaped trailing space belongs to the value, not DN formatting.
    while (end > 0 && /\s/u.test(text[end - 1])) {
        let slashes = 0;
        for (let index = end - 2; index >= 0 && text[index] === '\\'; index--) slashes++;
        if (slashes % 2 !== 0) break;
        end--;
    }
    return text.slice(0, end);
};

const hasValidEscapesAndQuotes = (text: string): boolean => {
    let quoted = false;
    for (let index = 0; index < text.length; index++) {
        const char = text[index];
        if (char === '\\') {
            const next = text[++index];
            if (!next) return false;
            if (/^[0-9a-f]$/iu.test(next)) {
                if (!/^[0-9a-f]$/iu.test(text[++index] ?? '')) return false;
            } else if (!' ,+"\\<>;=#'.includes(next)) return false;
        } else if (char === '"') {
            quoted = !quoted;
        } else if (char === '\0') return false;
    }
    return !quoted;
};

const findTopLevelChar = (source: string, targetChars: string | string[]): number => {
    const text = String(source || '');
    const targets = new Set(Array.isArray(targetChars) ? targetChars : [targetChars]);
    let escaped = false;
    let quoted = false;
    for (let index = 0; index < text.length; index++) {
        const char = text[index];
        if (escaped) {
            escaped = false;
            continue;
        }
        if (char === '\\') {
            escaped = true;
            continue;
        }
        if (char === '"') {
            quoted = !quoted;
            continue;
        }
        if (!quoted && targets.has(char)) {
            return index;
        }
    }
    return -1;
};

const splitTopLevel = (source: string, separator: string): string[] => {
    const text = String(source || '');
    const parts: string[] = [];
    let offset = 0;
    while (offset <= text.length) {
        const next = findTopLevelChar(text.slice(offset), separator);
        if (next < 0) {
            parts.push(text.slice(offset));
            break;
        }
        parts.push(text.slice(offset, offset + next));
        offset += next + 1;
    }
    return parts;
}

export interface DnSplit {
    rdn: string;
    parentDn: string;
}

export const splitFirstDnRdn = (dn: string): DnSplit => {
    const text = trimToString(dn);
    if (!text) return { rdn: '', parentDn: '' };
    const separatorIndex = findTopLevelChar(text, ',');
    if (separatorIndex < 0) {
        return { rdn: text, parentDn: '' };
    }
    return {
        rdn: trimToString(text.slice(0, separatorIndex)),
        parentDn: trimToString(text.slice(separatorIndex + 1)),
    };
};

const decodeEscapedValue = (value: string): string => {
    const text = String(value || '');
    let output = '';
    let escapedBytes: number[] = [];
    const flushEscapedBytes = () => {
        if (escapedBytes.length === 0) return;
        try {
            output += new TextDecoder().decode(new Uint8Array(escapedBytes));
        } catch {
            output += String.fromCharCode(...escapedBytes);
        }
        escapedBytes = [];
    };
    for (let index = 0; index < text.length; index++) {
        const char = text[index];
        if (char !== '\\') {
            flushEscapedBytes();
            output += char;
            continue;
        }
        const hex = text.slice(index + 1, index + 3);
        if (/^[0-9a-f]{2}$/iu.test(hex)) {
            escapedBytes.push(parseInt(hex, 16));
            index += 2;
            continue;
        }
        flushEscapedBytes();
        if (index + 1 < text.length) {
            output += text[index + 1];
            index += 1;
        }
    }
    flushEscapedBytes();
    return output;
};

/** Value of the first RDN of `dn` (escape-decoded), e.g. `cn=John Doe`. */
export const rdnConfirmationToken = (dn: string): string => {
    const { rdn } = splitFirstDnRdn(dn);
    if (!rdn) return '';
    const components = splitTopLevel(rdn, '+').map((part) => part.trim()).filter(Boolean);
    if (components.length !== 1) return rdn;
    const separatorIndex = findTopLevelChar(components[0], '=');
    if (separatorIndex < 0) return rdn;
    return decodeEscapedValue(components[0].slice(separatorIndex + 1)).trim();
};

export const isLikelyRdn = (value: unknown): boolean => {
    const rdn = trimToString(value);
    if (!rdn || !hasValidEscapesAndQuotes(rdn)) return false;
    if (findTopLevelChar(rdn, ',') >= 0) return false;
    const components = splitTopLevel(rdn, '+').map(trimToString);
    return components.every((component) => {
        const separatorIndex = findTopLevelChar(component, '=');
        if (separatorIndex <= 0) return false;
        const attribute = component.slice(0, separatorIndex).trim();
        if (!/^(?:[a-z][a-z0-9-]*|[0-9]+(?:\.[0-9]+)+)$/iu.test(attribute)) return false;
        return component.slice(separatorIndex + 1).trim().length > 0;
    });
};

export const isLikelyDn = (value: unknown): boolean => {
    const dn = trimToString(value);
    if (!dn) return false;
    return splitTopLevel(dn, ',').every((part) => isLikelyRdn(part));
};

/** Decode text AVAs for the directory fixture, reusing the DN escape parser. */
export const parseRdnAttributes = (rdn: string): Array<{ attribute: string; value: string }> => {
    if (!isLikelyRdn(rdn)) throw new Error('invalid RDN');
    return splitTopLevel(rdn, '+').map((component) => {
        const separator = findTopLevelChar(component, '=');
        const attribute = component.slice(0, separator).trim();
        let value = trimToString(component.slice(separator + 1));
        if (value.startsWith('#')) throw new Error('BER-encoded RDN values are not implemented in the fixture');
        if (value.startsWith('"') && value.endsWith('"')) value = value.slice(1, -1);
        return { attribute, value: decodeEscapedValue(value) };
    });
};

export const joinRdnAndParent = (rdn: string, parentDn: string): string => {
    const normalizedRdn = trimToString(rdn);
    const normalizedParent = trimToString(parentDn);
    if (!normalizedRdn) return '';
    return normalizedParent ? `${normalizedRdn},${normalizedParent}` : normalizedRdn;
};

/**
 * True when `dn` sits at or below `baseDn` (tiny-rdm ldap_service
 * `dnWithinBase` semantics, RFC 4519 suffix comparison on the RDN list).
 */
export const dnWithinBase = (dn: string, baseDn: string): boolean => {
    const child = trimToString(dn).replace(/,+$/u, '').toLowerCase();
    const base = trimToString(baseDn).replace(/,+$/u, '').toLowerCase();
    if (!base) return true;
    if (!child) return false;
    if (child === base) return true;
    return child.endsWith(`,${base}`);
};

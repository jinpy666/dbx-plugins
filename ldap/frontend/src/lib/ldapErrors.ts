/**
 * Friendly LDAP error mapping. Sidecar errors surface raw go-ldap strings
 * ("ldap search: LDAP Result Code 49 \"Invalid Credentials\": ..."), which
 * users cannot act on. `friendlyLdapError` maps the common result codes /
 * transport failures to localized, actionable text and passes anything
 * unknown through unchanged.
 */
import { t } from "./i18n";

const RULES: ReadonlyArray<{ pattern: RegExp; key: string }> = [
    // TLS 优先于 network：证书类错误也带 connection/handshake 字样
    { pattern: /certificate|x509|unknown authority|tls.*handshake/i, key: "err.tls" },
    { pattern: /result code 49|invalid credentials/i, key: "err.invalidCredentials" },
    { pattern: /result code 32|no such object/i, key: "err.noSuchObject" },
    { pattern: /result code 34|invalid dn syntax/i, key: "err.invalidDn" },
    { pattern: /result code 20|attribute or value exists/i, key: "err.attributeExists" },
    { pattern: /result code 19|constraint violation/i, key: "err.constraint" },
    { pattern: /result code 21|invalid attribute syntax/i, key: "err.invalidAttrSyntax" },
    { pattern: /result code 4|size limit exceeded/i, key: "err.sizeLimit" },
    { pattern: /administr?ative limit|result code 11/i, key: "err.adminLimit" },
    { pattern: /result code 53|unwilling to perform/i, key: "err.unwilling" },
    { pattern: /result code 8|strong(?:er)? auth(?:entication)? required/i, key: "err.strongAuth" },
    // 网络类先于通用超时：dial i/o timeout 归为"无法连接"更贴切
    {
        pattern: /result code 200|network error|connection refused|no such host|connection reset|broken pipe|i\/o timeout|eof/i,
        key: "err.network",
    },
    { pattern: /timeout|timed out|deadline exceeded/i, key: "err.timeout" },
];

export const friendlyLdapError = (message: string): string => {
    const raw = String(message ?? "");
    for (const rule of RULES) {
        if (rule.pattern.test(raw)) return t(rule.key);
    }
    return raw;
};

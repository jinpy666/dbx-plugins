// Exercise real manifests against the host's actual condition evaluator.
// No renderer or condition mock: this catches nested defaults and stale drafts.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { stripTypeScriptTypes } from "node:module";

const root = new URL("../../", import.meta.url);
const source = readFileSync(new URL("host/apps/desktop/src/lib/plugins/pluginFieldConditions.ts", root), "utf8");
const { pluginFieldIsVisible, pluginFieldIsRequired } = await import(
  `data:text/javascript;base64,${Buffer.from(stripTypeScriptTypes(source)).toString("base64")}`
);
const plugins = process.argv[2] ? [process.argv[2]] : ["ssh", "ldap", "files", "kafka"];
const locales = ["en", "zh-CN", "zh-TW", "es", "it", "ja", "pt-BR"];

for (const plugin of plugins) {
  assert(["ssh", "ldap", "files", "kafka"].includes(plugin));
  const manifest = JSON.parse(readFileSync(new URL(`${plugin}/manifest.json`, root), "utf8"));
  const provider = manifest.contributions.find((c) => c.type === "connection-provider");
  const fields = provider.fields;
  const byKey = Object.fromEntries(fields.map((field) => [field.key, field]));
  const defaults = Object.fromEntries(fields.map((field) => [field.key, field.default]));
  assert.equal(new Set(fields.map((field) => field.key)).size, fields.length, `${plugin}: duplicate field keys`);
  for (const [index, field] of fields.entries()) {
    for (const condition of [field.visible_when, field.required_when].filter(Boolean)) {
      const target = byKey[condition.field];
      assert(target, `${plugin}/${field.key}: unknown condition field`);
      assert(fields.indexOf(target) < index, `${plugin}/${field.key}: choose ${target.key} before its dependent input`);
      const validValues = target.type === "boolean" ? ["true", "false"] : target.options?.map((option) => option.value);
      if (validValues) assert(condition.one_of.every((value) => validValues.includes(value)), `${plugin}/${field.key}: invalid condition values`);
    }
    if (field.type === "select" && field.default !== undefined) {
      assert(field.options.some((option) => option.value === field.default), `${plugin}/${field.key}: invalid default`);
    }
    for (const locale of locales) {
      const localized = locale === "en" && !manifest.localizations.en
        ? field
        : manifest.localizations[locale]?.contributions[provider.id]?.fields[field.key];
      assert(localized?.label?.trim(), `${plugin}/${locale}/${field.key}: missing label`);
      for (const option of field.options ?? []) {
        const label = Array.isArray(localized.options)
          ? localized.options.find((item) => item.value === option.value)?.label
          : localized.options?.[option.value];
        assert(label?.trim(), `${plugin}/${locale}/${field.key}/${option.value}: missing option label`);
      }
    }
  }

  let scenarios = 0;
  function state(overrides) {
    scenarios++;
    const values = { ...defaults, ...overrides };
    const read = (key) => values[key];
    const visible = new Set(fields.filter((field) => pluginFieldIsVisible(field, read, (key) => byKey[key])).map((field) => field.key));
    const required = new Set(fields.filter((field) => visible.has(field.key) && pluginFieldIsRequired(field, read)).map((field) => field.key));
    return {
      visible(key, wanted) { assert.equal(visible.has(key), wanted, `${plugin}/${key} visibility: ${JSON.stringify(overrides)}`); },
      required(key, wanted) { assert.equal(required.has(key), wanted, `${plugin}/${key} required: ${JSON.stringify(overrides)}`); },
    };
  }
  const options = (key) => byKey[key].options.map((option) => option.value);

  if (plugin === "ssh") {
    for (const authentication of options("authentication")) for (const sudo_source of options("sudo_source")) {
      for (const auth_flow_mode of options("auth_flow_mode")) for (const read_only of [false, true]) {
        const s = state({ authentication, sudo_source, auth_flow_mode, read_only });
        const password = ["password", "private-key-password"].includes(authentication);
        const key = ["private-key", "private-key-password"].includes(authentication);
        s.visible("password", password); s.required("password", password);
        s.visible("private_key_path", key); s.required("private_key_path", key);
        s.visible("private_key_passphrase", key); s.required("private_key_passphrase", false);
        s.visible("agent_socket", authentication === "agent");
        s.visible("sudo_password", sudo_source === "custom");
        s.visible("sudo_profile", sudo_source === "global");
        // Read-only blocks sudo, but must not remove login-time 2FA settings.
        s.visible("auth_flow_mode", sudo_source !== "global");
        s.visible("totp_secret", sudo_source !== "global" && auth_flow_mode !== "password_only");
        s.visible("totp_prompt_hint", sudo_source !== "global" && auth_flow_mode !== "password_only");
      }
    }
    assert.equal(byKey.sudo_whitelist.type, "textarea");
  }

  if (plugin === "ldap") {
    for (const tls_mode of options("tls_mode")) for (const tls_verify of [true, false]) {
      for (const auth_type of options("auth_type")) for (const krb_credential_type of options("krb_credential_type")) {
        const s = state({ tls_mode, tls_verify, auth_type, krb_credential_type });
        s.visible("tls_verify", tls_mode !== "none");
        s.visible("tls_ca_path", tls_mode !== "none" && tls_verify);
        s.visible("tls_server_name", tls_mode !== "none");
        const password = ["simple", "ntlm", "digest_md5"].includes(auth_type);
        s.visible("bind_password", password); s.required("bind_password", password);
        s.visible("ntlm_hash", auth_type === "ntlm_hash");
        for (const [key, mode] of [["krb_password", "password"], ["krb_keytab_path", "keytab"], ["krb_ccache_path", "ccache"]]) {
          const enabled = auth_type === "kerberos" && krb_credential_type === mode;
          s.visible(key, enabled); s.required(key, enabled);
        }
        s.required("port", false);
      }
    }
    assert.equal(byKey.port.default, undefined, "port must allow the TLS scheme's default");
  }

  if (plugin === "files") {
    for (const protocol of options("protocol")) for (const read_only of [true, false]) {
      const s = state({ protocol, read_only, connection_mode: "via-dbx-ssh" });
      s.visible("user", ["ftp", "sftp", "sftp-native"].includes(protocol));
      s.visible("username", ["webdav", "smb"].includes(protocol));
      s.visible("password", ["webdav", "ftp", "smb", "sftp-native"].includes(protocol));
      s.visible("key", ["sftp", "sftp-native"].includes(protocol));
      s.visible("known_hosts_strategy", ["sftp", "sftp-native"].includes(protocol));
      s.visible("endpoint", !["fs", "opendal-custom"].includes(protocol));
      s.required("endpoint", ["webdav", "ftp", "sftp", "sftp-native", "smb"].includes(protocol));
      s.visible("allow_delete", !read_only);
      s.visible("config", protocol === "opendal-custom");
      s.visible("connection_mode", false); s.visible("dbx_ssh_connection", false);
      s.required("secret_access_key", ["s3", "oss"].includes(protocol));
    }
    assert.equal(byKey.key.binding, "secret");
    assert.equal(byKey.key.type, "textarea");
  }

  if (plugin === "kafka") {
    for (const connection_source of options("connection_source")) for (const security_protocol of options("security_protocol")) {
      for (const sasl_mechanism of options("sasl_mechanism")) for (const oauth_token_source of [undefined, ...options("oauth_token_source")]) {
        for (const schema_registry of options("schema_registry")) for (const glue_auth_mode of options("glue_auth_mode")) {
          const s = state({ connection_source, security_protocol, sasl_mechanism, oauth_token_source, schema_registry, glue_auth_mode });
          s.visible("bootstrap_servers", connection_source === "bootstrap");
          s.required("bootstrap_servers", connection_source === "bootstrap");
          s.visible("zk_servers", connection_source === "zookeeper");
          s.required("zk_servers", connection_source === "zookeeper");
          const sasl = security_protocol.startsWith("SASL_");
          const password = sasl && ["PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512"].includes(sasl_mechanism);
          s.visible("sasl_username", password); s.required("sasl_username", password);
          s.visible("sasl_password", password); s.required("sasl_password", password);
          const kerberos = sasl && sasl_mechanism === "GSSAPI";
          s.visible("kerberos_keytab_path", kerberos); s.required("kerberos_principal", kerberos);
          const oauth = sasl && sasl_mechanism === "OAUTHBEARER";
          s.visible("oauth_token_source", oauth); s.required("oauth_token_source", oauth);
          s.visible("msk_region", oauth && oauth_token_source === "msk_iam");
          s.required("msk_region", oauth && oauth_token_source === "msk_iam");
          s.visible("oauth_static_token", oauth && oauth_token_source === "static_token");
          s.required("oauth_static_token", oauth && oauth_token_source === "static_token");
          for (const key of ["tls_ca_cert", "tls_client_cert", "tls_client_key", "tls_insecure_skip_verify"]) {
            s.visible(key, ["SSL", "SASL_SSL"].includes(security_protocol));
          }
          s.visible("sr_url", schema_registry === "confluent"); s.required("sr_url", schema_registry === "confluent");
          s.visible("glue_region", schema_registry === "aws_glue");
          s.visible("glue_secret_access_key", schema_registry === "aws_glue" && glue_auth_mode === "static");
          s.required("glue_secret_access_key", schema_registry === "aws_glue" && glue_auth_mode === "static");
        }
      }
    }
    assert.equal(byKey.oauth_token_source.default, undefined, "keep the older-host default fallback safe");
    assert.equal(byKey.tls_client_key.type, "textarea");
    assert.equal(byKey.tls_client_key.binding, "secret");
    state({ read_only: true }).visible("allow_delete", false);
    state({ read_only: false }).visible("allow_delete", true);
  }
  console.log(`PASS ${plugin}: ${scenarios} connection combinations; field ordering and seven-language labels/options`);
}

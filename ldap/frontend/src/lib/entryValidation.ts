import { mustAttributesFor, type LdapSchema } from "./newEntryTemplates";

type Attributes = Record<string, string[]>;
const keyOf = (name: string) => name.split(";")[0].trim().toLowerCase();

function valuesFor(attributes: Attributes, name: string): string[] {
  const key = keyOf(name);
  return Object.entries(attributes)
    .filter(([attribute]) => keyOf(attribute) === key)
    .flatMap(([, values]) => values)
    .filter((value) => value.trim() !== "");
}

/**
 * Preflight only requirements affected by this edit. Existing entries may omit
 * MUST attributes because of ACLs/policy; their absence alone must not force a
 * replacement value. Adding an objectClass can introduce new requirements.
 */
export function missingRequiredAttributes(current: Attributes, original?: Attributes, schema?: LdapSchema): string[] {
  const required = ["objectClass", ...mustAttributesFor(valuesFor(current, "objectClass"), schema)];
  const previouslyRequired = new Set([
    "objectclass",
    ...mustAttributesFor(valuesFor(original ?? {}, "objectClass"), schema).map(keyOf),
  ]);
  return required.filter((name) => {
    const previouslyVisible = original !== undefined && valuesFor(original, name).length > 0;
    // An objectClass edit must never force replacement of an unreadable secret.
    if (original && !previouslyVisible && /password|unicodepwd/iu.test(keyOf(name))) return false;
    return valuesFor(current, name).length === 0 && (
      original === undefined || previouslyVisible || !previouslyRequired.has(keyOf(name))
    );
  });
}

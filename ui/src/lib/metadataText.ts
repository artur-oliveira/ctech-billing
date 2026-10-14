/** The most keys, and the longest key and value, billing accepts (ADR 0008). */
export const METADATA_MAX_KEYS = 50;
export const METADATA_MAX_KEY = 40;
export const METADATA_MAX_VALUE = 500;

export type MetadataParse =
  | {ok: true; value: Record<string, string>}
  | {ok: false; line: number; reason: "format" | "duplicate" | "key" | "value" | "count"}

/**
 * "chave=valor", one per line. Blank lines are skipped; the value may itself
 * contain "=". A line without a key, a repeated key, or a value or key over
 * billing's limits is reported with its 1-based line, so the form can say which.
 */
export function parseMetadataText(text: string): MetadataParse {
  const value: Record<string, string> = {};
  const lines = text.split("\n");
  for (let i = 0; i < lines.length; i++) {
    const raw = lines[i].trim();
    if (raw === "") continue;
    const at = raw.indexOf("=");
    const key = at < 0 ? "" : raw.slice(0, at).trim();
    if (at < 0 || key === "") return {ok: false, line: i + 1, reason: "format"};
    const val = raw.slice(at + 1).trim();
    if ([...key].length > METADATA_MAX_KEY) return {ok: false, line: i + 1, reason: "key"};
    if ([...val].length > METADATA_MAX_VALUE) return {ok: false, line: i + 1, reason: "value"};
    if (key in value) return {ok: false, line: i + 1, reason: "duplicate"};
    value[key] = val;
    if (Object.keys(value).length > METADATA_MAX_KEYS) return {ok: false, line: i + 1, reason: "count"};
  }
  return {ok: true, value};
}

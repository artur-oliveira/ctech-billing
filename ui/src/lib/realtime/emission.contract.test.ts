import {readFileSync} from "node:fs";
import {resolve} from "node:path";

import {describe, expect, it} from "vitest";

import {effectsOf, type Surface} from "./catalogue";
import type {ChangeView} from "./codec";

// docs/realtime/events.md is the list the Go emitter is checked against too
// (api/internal/realtime/events_doc_test.go). Here: every row's surface columns
// agree with what effectsOf does, and every documented type has a mapping.

const ORG = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b";
const SURFACES: Record<"console" | "portal" | "finance" | "checkout", Surface> = {
  console: {kind: "console", scope: {org: ORG, mode: "live"}},
  portal: {kind: "portal"},
  finance: {kind: "finance", mode: "live", space: {kind: "organization", organizationId: ORG}},
  checkout: {kind: "checkout", token: "tok"},
};

interface Row { entity: string; types: string[]; columns: Record<keyof typeof SURFACES, string> }

function rows(): Row[] {
  const text = readFileSync(resolve(__dirname, "../../../../docs/realtime/events.md"), "utf8");
  const table = text.slice(text.indexOf("<!-- events:begin -->"), text.indexOf("<!-- events:end -->"));
  return table.split("\n")
    .filter((l) => l.startsWith("|") && !l.startsWith("|---") && !l.startsWith("| entity"))
    .map((l) => {
      const c = l.replace(/^\||\|\s*$/g, "").split("|").map((s) => s.trim());
      return {entity: c[0], types: c[1].split(",").map((s) => s.trim()), columns: {console: c[3], portal: c[4], finance: c[5], checkout: c[6]}};
    });
}

const change = (entity: string, type: string): ChangeView =>
  ({id: "01A", type, entity, entityId: "x_1", status: "S", revision: 1, occurredAt: 1}) as ChangeView;

describe("events.md and the UI catalogue", () => {
  it("has rows", () => expect(rows().length).toBeGreaterThan(5));

  for (const row of rows()) {
    for (const [name, surface] of Object.entries(SURFACES)) {
      const cell = row.columns[name as keyof typeof SURFACES];
      it(`${row.entity} on ${name}: ${cell}`, () => {
        for (const type of row.types) {
          const invalidated = effectsOf(surface, change(row.entity, type)).invalidate;
          if (cell === "none") expect(invalidated, `${type} must not invalidate`).toEqual([]);
          else expect(invalidated.length, `${type} must invalidate something (${cell})`).toBeGreaterThan(0);
        }
      });
    }
  }
});

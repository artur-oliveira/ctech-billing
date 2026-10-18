import "@testing-library/jest-dom/vitest";

import {render, screen} from "@testing-library/react";
import {describe, expect, it} from "vitest";

import {BUCKET_STATUS, IMPORT_STATUS, LEDGER_STATUS, RECURRENCE_STATUS, StatusBadge} from "@/components/finance/StatusBadge";
import {STATUS} from "@/components/finance/statementStatus";

describe("StatusBadge", () => {
  it("draws the word with a glyph that is hidden from a screen reader", () => {
    render(<StatusBadge status={RECURRENCE_STATUS.pending}>2 aguardando</StatusBadge>);
    const badge = screen.getByText("2 aguardando");
    expect(badge).toHaveAttribute("data-tone", "attention");
    expect(badge.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
  });

  it("every state of every map carries a glyph, never colour alone", () => {
    const all = [...Object.values(BUCKET_STATUS), ...Object.values(RECURRENCE_STATUS), ...Object.values(LEDGER_STATUS), ...Object.values(IMPORT_STATUS), ...Object.values(STATUS)];
    expect(all.length).toBeGreaterThan(10);
    for (const s of all) expect(s.icon).toBeTruthy();
  });
});

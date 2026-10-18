import "@testing-library/jest-dom/vitest";

import {render, screen} from "@testing-library/react";
import {describe, expect, it} from "vitest";

import {Amount, AmountRow} from "@/components/ui/Amount";

describe("Amount", () => {
  it("switches on tabular figures and applies the tone", () => {
    render(<Amount tone="danger" strong>R$ 10,00</Amount>);
    const el = screen.getByText("R$ 10,00");
    expect(el).toHaveAttribute("data-numeric");
    expect(el).toHaveClass("text-danger", "font-medium");
  });
  it("a struck amount is muted and crossed out whatever its tone", () => {
    render(<Amount tone="danger" struck>R$ 1,00</Amount>);
    expect(screen.getByText("R$ 1,00")).toHaveClass("text-muted-foreground", "line-through");
    expect(screen.getByText("R$ 1,00")).not.toHaveClass("text-danger");
  });
  it("has no class attribute when it adds nothing", () => {
    render(<Amount>1</Amount>);
    expect(screen.getByText("1")).not.toHaveAttribute("class");
  });
});

describe("AmountRow", () => {
  it("is a dt/dd pair by default", () => {
    render(<dl><AmountRow label="Entradas"><Amount>R$ 5,00</Amount></AmountRow></dl>);
    expect(screen.getByText("Entradas").tagName).toBe("DT");
    expect(screen.getByText("R$ 5,00").parentElement?.tagName).toBe("DD");
  });
  it("is a list item as `li`, and a total is medium weight", () => {
    render(<ul><AmountRow as="li" strong label="Total"><Amount>R$ 9,00</Amount></AmountRow></ul>);
    expect(screen.getByRole("listitem")).toHaveClass("font-medium");
  });
});

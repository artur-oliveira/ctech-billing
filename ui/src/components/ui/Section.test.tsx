import "@testing-library/jest-dom/vitest";

import {render, screen} from "@testing-library/react";
import {describe, expect, it} from "vitest";

import {Section} from "@/components/ui/Section";

describe("Section", () => {
  it("names its region by the heading, with a unique id per instance", () => {
    render(<><Section title="Saldos">a</Section><Section title="Saldos" action={<a href="/x">Ver</a>}>b</Section></>);
    const regions = screen.getAllByRole("region", {name: "Saldos"});
    expect(regions).toHaveLength(2);
    const ids = screen.getAllByRole("heading", {level: 2}).map(h => h.id);
    expect(new Set(ids).size).toBe(2);
    expect(screen.getByRole("link", {name: "Ver"})).toBeInTheDocument();
  });
  it("hides the title visually but still names the region", () => {
    render(<Section title="Projeção" hideTitle>x</Section>);
    expect(screen.getByRole("region", {name: "Projeção"})).toBeInTheDocument();
    expect(screen.getByRole("heading", {name: "Projeção"})).toHaveClass("sr-only");
  });
  it("keeps accents and punctuation in the title out of the id", () => {
    render(<Section title="Vencidas e próximas (5)">x</Section>);
    expect(screen.getByRole("region", {name: "Vencidas e próximas (5)"})).toBeInTheDocument();
  });
  it("draws the heading in foreground ink, or muted on request", () => {
    render(<><Section title="A">x</Section><Section title="B" tone="muted">y</Section></>);
    expect(screen.getByRole("heading", {name: "A"})).toHaveClass("text-foreground");
    expect(screen.getByRole("heading", {name: "B"})).toHaveClass("text-muted-foreground");
  });
});

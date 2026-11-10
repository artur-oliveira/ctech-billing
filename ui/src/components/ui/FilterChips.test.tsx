import "@testing-library/jest-dom/vitest";

import {render, screen} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {describe, expect, it, vi} from "vitest";

import {FilterChips} from "@/components/ui/FilterChips";

const OPTIONS = ["a", "b", "c", "d", "e", "f", "g"].map(v => ({value: v, label: v.toUpperCase()}));

describe("FilterChips", () => {
  it("shows every option of a long list, marking the chosen one", () => {
    render(<FilterChips label="Status" value="c" onValueChange={() => {}} options={OPTIONS}/>);
    expect(screen.getByRole("group", {name: "Status"})).toBeInTheDocument();
    expect(screen.getAllByRole("button")).toHaveLength(7);
    expect(screen.getByRole("button", {name: "C"})).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("button", {name: "A"})).toHaveAttribute("aria-pressed", "false");
  });

  it("reports the chip pressed", async () => {
    const change = vi.fn();
    render(<FilterChips label="Status" value="a" onValueChange={change} options={OPTIONS}/>);
    await userEvent.click(screen.getByRole("button", {name: "F"}));
    expect(change).toHaveBeenCalledWith("f");
  });
});

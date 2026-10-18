import "@testing-library/jest-dom/vitest";
import {render, screen} from "@testing-library/react";
import {describe, expect, it} from "vitest";

import {Checkbox} from "@/components/ui/Checkbox";

describe("Checkbox", () => {
  it("carries the dash styling that replaces the check glyph when indeterminate", () => {
    render(<Checkbox indeterminate aria-label="todas" className="extra"/>);
    const box = screen.getByRole("checkbox", {name: "todas"});
    expect(box).toHaveAttribute("aria-checked", "mixed");
    expect(box.className).toContain("data-[indeterminate]:[&_svg]:hidden");
    expect(box.className).toContain("extra");
  });
});

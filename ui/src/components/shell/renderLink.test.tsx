import "@testing-library/jest-dom/vitest";

import {render, screen} from "@testing-library/react";
import {describe, expect, it} from "vitest";

import {renderNavLink} from "@/components/shell/renderLink";

describe("renderNavLink", () => {
  it("keeps the rail's classes and makes the row a 44px target under touch", () => {
    render(renderNavLink({href: "/finance", className: "min-h-9", children: "Resumo"}));
    const link = screen.getByRole("link", {name: "Resumo"});
    expect(link).toHaveAttribute("href", "/finance");
    expect(link.className).toContain("min-h-9");
    expect(link.className).toContain("touch:min-h-11!");
  });
});

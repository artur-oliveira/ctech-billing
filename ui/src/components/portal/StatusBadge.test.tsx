import {screen} from "@testing-library/react";
import {describe, expect, it} from "vitest";

import {renderWithQuery} from "@/components/finance/finance.test-utils";

import {StatusBadge} from "./StatusBadge";

describe("StatusBadge", () => {
  it("never shows a raw status code the catalog does not know", () => {
    renderWithQuery(<StatusBadge state="some_new_enum" tone="neutral"/>);
    expect(screen.queryByText("some_new_enum")).toBeNull();
    expect(screen.getByText("Em atualização")).toBeTruthy();
  });
});

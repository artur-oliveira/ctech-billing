import {render, screen} from "@testing-library/react";
import {describe, expect, it} from "vitest";

import {ImportStep} from "@/components/finance/ImportStep";

describe("ImportStep", () => {
  it("names its landmark by the step heading and shows the hint and content", () => {
    render(<ImportStep n={2} title="Review" hint="Nothing is posted yet"><p>lines</p></ImportStep>);
    expect(screen.getByRole("region", {name: "Review"})).toBeTruthy();
    expect(screen.getByRole("heading", {name: "Review", level: 2})).toBeTruthy();
    expect(screen.getByText("Nothing is posted yet")).toBeTruthy();
    expect(screen.getByText("lines")).toBeTruthy();
  });
});

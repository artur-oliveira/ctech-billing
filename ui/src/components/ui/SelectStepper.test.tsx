import "@testing-library/jest-dom/vitest";

import {render, screen} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {useState} from "react";
import {describe, expect, it} from "vitest";

import {SelectStepper} from "@/components/ui/SelectStepper";

const OPTIONS = [{value: "a", label: "A"}, {value: "b", label: "B"}, {value: "c", label: "C"}];

function Harness({start}: {start: string}) {
  const [v, setV] = useState(start);
  return <SelectStepper label="Período" prevLabel="Anterior" nextLabel="Próximo" value={v} onValueChange={setV} options={OPTIONS}/>;
}

describe("SelectStepper", () => {
  it("steps through the options and disables the chevron at each end", async () => {
    render(<Harness start="a"/>);
    expect(screen.getByRole("button", {name: "Anterior"})).toBeDisabled();
    await userEvent.click(screen.getByRole("button", {name: "Próximo"}));
    expect(screen.getByRole("combobox", {name: "Período"})).toHaveTextContent("B");
    await userEvent.click(screen.getByRole("button", {name: "Próximo"}));
    expect(screen.getByRole("button", {name: "Próximo"})).toBeDisabled();
    await userEvent.click(screen.getByRole("button", {name: "Anterior"}));
    expect(screen.getByRole("combobox", {name: "Período"})).toHaveTextContent("B");
  });
});

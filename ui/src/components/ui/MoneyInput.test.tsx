import "@testing-library/jest-dom/vitest";

import {render, screen} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {useState} from "react";
import {describe, expect, it} from "vitest";

import {MoneyInput} from "@/components/ui/MoneyInput";

function Harness({signed}: {signed?: boolean}) {
  const [v, setV] = useState("");
  return <MoneyInput aria-label="Valor" value={v} onValueChange={setV} signed={signed}/>;
}

describe("MoneyInput", () => {
  it("asks for a decimal keypad, hints the locale's zero, and masks as you type", async () => {
    render(<Harness/>);
    const input = screen.getByLabelText("Valor");
    expect(input).toHaveAttribute("inputmode", "decimal");
    expect(input).toHaveAttribute("placeholder", "0,00");
    await userEvent.type(input, "1234,56");
    expect(input).toHaveValue("1.234,56");
  });
  it("accepts a minus sign only when signed", async () => {
    const {unmount} = render(<Harness/>);
    await userEvent.type(screen.getByLabelText("Valor"), "-5");
    expect(screen.getByLabelText("Valor")).toHaveValue("5");
    unmount();
    render(<Harness signed/>);
    await userEvent.type(screen.getByLabelText("Valor"), "-5");
    expect(screen.getByLabelText("Valor")).toHaveValue("-5");
  });
});

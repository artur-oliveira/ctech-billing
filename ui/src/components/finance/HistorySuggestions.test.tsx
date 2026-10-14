import "@testing-library/jest-dom/vitest";

import {render, screen} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {useState} from "react";
import {describe, expect, it, vi} from "vitest";

import {type HistoryEntry, SuggestingInput} from "@/components/finance/HistorySuggestions";

const ENTRIES: HistoryEntry[] = [
  {description: "Mercado Central", amount: 12990, categoryId: "food"},
  {description: "Mercado Livre", amount: 4590, categoryId: "shop"},
  {description: "Padaria", amount: 1800, categoryId: "food"},
];

function Harness({onSelect = () => {}, onSubmit = () => {}}: {onSelect?: (e: HistoryEntry) => void; onSubmit?: () => void}) {
  const [value, setValue] = useState("");
  return (
    <form onSubmit={e => { e.preventDefault(); onSubmit(); }}>
      <label htmlFor="d">Descrição</label>
      <SuggestingInput id="d" value={value} onValueChange={setValue} entries={ENTRIES} onSelect={e => { setValue(e.description); onSelect(e); }}/>
      <button>fora</button>
    </form>
  );
}

describe("SuggestingInput", () => {
  it("floats over the form: the list takes no room in the layout", async () => {
    render(<Harness/>);
    await userEvent.type(screen.getByRole("combobox", {name: "Descrição"}), "merc");
    const list = screen.getByRole("listbox");
    expect(list).toHaveClass("absolute");
    expect(screen.getAllByRole("option").map(o => o.textContent)).toEqual([expect.stringContaining("Mercado Central"), expect.stringContaining("Mercado Livre")]);
    expect(screen.getAllByRole("option")[0]).toHaveTextContent("129,90");
  });

  it("shows nothing without a prefix, without a match, or once the field loses focus", async () => {
    render(<Harness/>);
    const input = screen.getByRole("combobox", {name: "Descrição"});
    await userEvent.click(input);
    expect(screen.queryByRole("listbox")).toBeNull();
    await userEvent.type(input, "zzz");
    expect(screen.queryByRole("listbox")).toBeNull();
    await userEvent.clear(input);
    await userEvent.type(input, "pad");
    expect(screen.getByRole("listbox")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", {name: "fora"}));
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  it("choosing by click fills through onSelect, closes, and keeps the focus in the field", async () => {
    const onSelect = vi.fn();
    render(<Harness onSelect={onSelect}/>);
    const input = screen.getByRole("combobox", {name: "Descrição"});
    await userEvent.type(input, "merc");
    await userEvent.click(screen.getAllByRole("option")[1]);
    expect(onSelect).toHaveBeenCalledWith(ENTRIES[1]);
    expect(input).toHaveValue("Mercado Livre");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(input).toHaveFocus();
  });

  it("arrows then Enter choose; Enter with nothing highlighted still submits the form", async () => {
    const onSelect = vi.fn();
    const onSubmit = vi.fn();
    render(<Harness onSelect={onSelect} onSubmit={onSubmit}/>);
    const input = screen.getByRole("combobox", {name: "Descrição"});
    await userEvent.type(input, "merc{Enter}");
    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(onSelect).not.toHaveBeenCalled();
    await userEvent.type(input, "{ArrowDown}{ArrowDown}");
    expect(input).toHaveAttribute("aria-activedescendant", screen.getAllByRole("option")[1].id);
    await userEvent.keyboard("{Enter}");
    expect(onSelect).toHaveBeenCalledWith(ENTRIES[1]);
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it("Escape closes the list and nothing else (the drawer around it stays)", async () => {
    const outer = vi.fn();
    render(<div onKeyDown={outer}><Harness/></div>);
    const input = screen.getByRole("combobox", {name: "Descrição"});
    await userEvent.type(input, "merc");
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(input).toHaveValue("merc");
    expect(outer).not.toHaveBeenCalledWith(expect.objectContaining({key: "Escape"}));
    await userEvent.type(input, "a");
    expect(screen.getByRole("listbox")).toBeInTheDocument();
  });
});

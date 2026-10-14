import "@testing-library/jest-dom/vitest";

import {render, screen} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {useState} from "react";
import {describe, expect, it} from "vitest";

import {ConsoleScopeProvider} from "@/lib/console/scope";

const A = {mode: "live", org: "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} as const;
const B = {mode: "live", org: "0190a1b2-c3d4-7e5f-8a9b-bbbbbbbbbbbb"} as const;

function Draft() {
  const [text, setText] = useState("");
  return <input aria-label="draft" value={text} onChange={e => setText(e.target.value)}/>;
}

// Final review: a switch made in another tab flips this tab's organization
// underneath an open form; the form must not carry org A's draft into org B.
describe("ConsoleScopeProvider", () => {
  it("remounts its subtree when the organization or the mode changes, dropping drafts", async () => {
    const {rerender} = render(<ConsoleScopeProvider value={A}><Draft/></ConsoleScopeProvider>);
    await userEvent.type(screen.getByLabelText("draft"), "produto da A");
    rerender(<ConsoleScopeProvider value={{...A}}><Draft/></ConsoleScopeProvider>);
    expect(screen.getByLabelText("draft")).toHaveValue("produto da A");
    rerender(<ConsoleScopeProvider value={B}><Draft/></ConsoleScopeProvider>);
    expect(screen.getByLabelText("draft")).toHaveValue("");
    await userEvent.type(screen.getByLabelText("draft"), "x");
    rerender(<ConsoleScopeProvider value={{...B, mode: "test"}}><Draft/></ConsoleScopeProvider>);
    expect(screen.getByLabelText("draft")).toHaveValue("");
  });
});

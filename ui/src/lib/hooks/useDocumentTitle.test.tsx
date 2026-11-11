import {render} from "@testing-library/react";
import {afterEach, describe, expect, it} from "vitest";
import {useDocumentTitle} from "./useDocumentTitle";

function Title({title}: {title: string | null}) {
  useDocumentTitle(title);
  return null;
}

const flush = () => new Promise(r => setTimeout(r, 0));

describe("useDocumentTitle", () => {
  afterEach(() => { document.title = ""; });

  it("sets the title with the product suffix", () => {
    render(<Title title="Cards"/>);
    expect(document.title).toBe("Cards · CTech Billing");
  });

  it("wins over a framework that rewrites the title afterwards", async () => {
    document.head.appendChild(document.createElement("title"));
    render(<Title title="Cards"/>);
    document.title = "Cartões"; // what Next's metadata does after the effect
    await flush();
    expect(document.title).toBe("Cards · CTech Billing");
  });

  it("wins when the framework replaces the <title> node itself", async () => {
    const first = document.createElement("title");
    document.head.appendChild(first);
    render(<Title title="Cards"/>);
    first.remove();
    const second = document.createElement("title");
    second.textContent = "Cartões";
    document.head.appendChild(second);
    await flush();
    expect(document.title).toBe("Cards · CTech Billing");
  });

  it("leaves the title alone for null and after unmount", async () => {
    document.title = "Cartões";
    const {unmount, rerender} = render(<Title title={null}/>);
    expect(document.title).toBe("Cartões");
    rerender(<Title title="Cards"/>);
    unmount();
    document.title = "Cartões";
    await flush();
    expect(document.title).toBe("Cartões");
  });
});

import "@testing-library/jest-dom/vitest";

import {render, screen} from "@testing-library/react";
import {describe, expect, it} from "vitest";

import {StatementTotal} from "@/components/finance/StatementTotal";

describe("StatementTotal", () => {
  it("is just the figure when nothing is forecast", () => {
    render(<StatementTotal posted={10000}/>);
    expect(screen.getByText("R$ 100,00")).toBeInTheDocument();
    expect(screen.queryByText(/recorrências/)).toBeNull();
  });
  it("heads with posted plus forecast and says the two parts beneath", () => {
    render(<StatementTotal posted={10000} forecast={34000}/>);
    expect(screen.getByText("R$ 440,00")).toBeInTheDocument();
    expect(screen.getByText("já lançado R$ 100,00 · recorrências previstas R$ 340,00")).toBeInTheDocument();
  });
  it("says when the forecast is still being read or is unavailable, instead of passing the posted part off as the month", () => {
    const {rerender} = render(<StatementTotal posted={10000} forecastState="loading"/>);
    expect(screen.getByText(/lendo recorrências previstas/)).toBeInTheDocument();
    rerender(<StatementTotal posted={10000} forecastState="error"/>);
    expect(screen.getByText(/recorrências previstas indisponíveis/)).toBeInTheDocument();
  });
  it("names a credit", () => {
    render(<StatementTotal posted={-2500}/>);
    expect(screen.getByText("crédito a seu favor")).toBeInTheDocument();
  });
  it("marks the forecast estimado when some of it is an expected amount", () => {
    render(<StatementTotal posted={0} forecast={5000} estimated/>);
    expect(screen.getByText(/recorrências previstas ~R\$\s*50,00 \(estimado\)/)).toBeInTheDocument();
  });
});

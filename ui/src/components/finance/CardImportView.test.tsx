import "@testing-library/jest-dom/vitest";

import {fireEvent, screen, waitFor, within} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {afterEach, describe, expect, it, vi} from "vitest";

import {CardImportView} from "@/components/finance/CardImportView";
import {pick, renderWithQuery} from "@/components/finance/finance.test-utils";
import * as finance from "@/lib/api/finance";
import type {Account, Card, CardImportDetail, CardImportLine, CardSuggestion, ImportSummary, Verb} from "@/lib/api/financeTypes";

const ALL: Verb[] = ["finance.read", "finance.write", "finance.settle", "finance.import", "finance.configure"];
const ACCOUNTS: Account[] = [
  {id: "food", name: "Mercado", class: "expense", dre_group: "operating_expenses", system: false, archived: false, balance: 0},
  {id: "fuel", name: "Combustível", class: "expense", dre_group: "operating_expenses", system: false, archived: false, balance: 0},
];
const CARD: Card = {id: "inter", name: "Inter", closing_day: 1, due_day: 7, paying_account_id: "cc", open_month: "2026-12", balance: 0, archived: false};

const line = (n: number, over: Partial<CardImportLine> = {}): CardImportLine => ({
  n, date: "2026-10-29", amount: 9051, description: `LINHA ${n}`, status: "pending", bank_category: "SUPERMERCADO", ...over,
});
const item = {purchase_id: "p1", tag: "01", description: "Mercado do bairro", amount: 9051, date: "2026-10-29", number: 1, of: 1};

function detail(lines: CardImportLine[], suggestions: CardSuggestion[] = []): CardImportDetail {
  return {
    import: {id: "i1", account_id: "inter", format: "inter_csv", kind: "card", statement_month: "2026-12", created_at: "2026-11-02T10:00:00Z",
      lines: lines.length, duplicates: 0, rejected_count: 0, rejected: [], pending: lines.filter(l => l.status === "pending").length},
    lines, suggestions,
  };
}

function serve(d: CardImportDetail, categories: Record<string, string> = {}, imports: ImportSummary[] = []) {
  vi.spyOn(finance, "listImports").mockResolvedValue({data: imports, has_more: false});
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs: ALL, manage_people: false}], organizations_unavailable: false});
  vi.spyOn(finance, "getCardImport").mockResolvedValue(d);
  vi.spyOn(finance, "getBankCategories").mockResolvedValue({categories});
  vi.spyOn(finance, "getCardStatement").mockResolvedValue({
    card_id: "inter", month: "2026-12", status: "open", closing_date: "2026-12-01", due_date: "2026-12-07", total: 9051, items: [],
  });
  return {
    confirm: vi.spyOn(finance, "confirmCardLines").mockResolvedValue({lines: []}),
    confirmAll: vi.spyOn(finance, "confirmAllCardLines").mockResolvedValue({confirmed: 1}),
    create: vi.spyOn(finance, "createCardLine").mockResolvedValue({line: line(1, {status: "created"})}),
    ignore: vi.spyOn(finance, "ignoreLine").mockResolvedValue({line: {} as never}),
    put: vi.spyOn(finance, "putBankCategories").mockResolvedValue({categories: {}}),
    reopen: vi.spyOn(finance, "reopenStatement").mockResolvedValue({} as never),
    upload: vi.spyOn(finance, "uploadCardImport").mockResolvedValue({} as never),
  };
}

afterEach(() => vi.restoreAllMocks());

describe("importar fatura do cartão", () => {
  it("suggests the statement month from the file name, starting from the card's open month", async () => {
    serve(detail([]));
    renderWithQuery(<CardImportView card={CARD} accounts={ACCOUNTS} onClose={() => {}}/>);
    const month = await screen.findByLabelText("Fatura");
    expect(month).toHaveValue("2026-12");
    await userEvent.upload(screen.getByLabelText(/Arquivo/), new File(["x"], "fatura-inter-2026-11.csv", {type: "text/csv"}));
    expect(month).toHaveValue("2026-11");
  });

  it("offers to reopen the latest closed statement and does not offer the upload", async () => {
    const s = serve(detail([]));
    renderWithQuery(<CardImportView card={CARD} accounts={ACCOUNTS} onClose={() => {}}/>);
    const month = await screen.findByLabelText("Fatura");
    fireEvent.change(month, {target: {value: "2026-11"}});
    expect(screen.queryByRole("button", {name: "Importar"})).not.toBeInTheDocument();
    await userEvent.click(await screen.findByRole("button", {name: "Reabrir fatura"}));
    await userEvent.click(await screen.findByRole("button", {name: "Confirmar"}));
    await waitFor(() => expect(s.reopen).toHaveBeenCalledWith(expect.anything(), "inter", "2026-11", expect.any(String)));
  });

  it("says an older statement cannot be imported into", async () => {
    serve(detail([]));
    renderWithQuery(<CardImportView card={CARD} accounts={ACCOUNTS} onClose={() => {}}/>);
    const month = await screen.findByLabelText("Fatura");
    fireEvent.change(month, {target: {value: "2026-09"}});
    expect(await screen.findByText(/Só a fatura aberta recebe importação/)).toBeInTheDocument();
    expect(screen.queryByRole("button", {name: "Reabrir fatura"})).not.toBeInTheDocument();
  });

  it("lists pairs, confirms one, and confirms all in one call", async () => {
    const s = serve(detail([line(1), line(2, {amount: 2000, description: "POSTO"})], [
      {kind: "pair", lines: [1], item}, {kind: "pair", lines: [2], item: {...item, purchase_id: "p2", amount: 2000, description: "Gasolina"}},
    ]));
    renderWithQuery(<CardImportView card={CARD} accounts={ACCOUNTS} importId="i1" onClose={() => {}}/>);
    const first = (await screen.findByText("Mercado do bairro")).closest("li") as HTMLElement;
    expect(within(first).getByText(/LINHA 1/)).toBeInTheDocument();
    await userEvent.click(within(first).getByRole("button", {name: "Confirmar"}));
    await waitFor(() => expect(s.confirm).toHaveBeenCalledWith(expect.anything(), "i1", {lines: [1], purchase_id: "p1", tag: "01"}, expect.any(String)));
    await userEvent.click(screen.getByRole("button", {name: "Confirmar todos (2)"}));
    expect(s.confirmAll).not.toHaveBeenCalled(); // asks first
    await userEvent.click(screen.getAllByRole("button", {name: "Confirmar"}).at(-1)!);
    await waitFor(() => expect(s.confirmAll).toHaveBeenCalledTimes(1));
  });

  it("creates a purchase only once the bank category is mapped, and maps it from the screen", async () => {
    const s = serve(detail([line(1)]));
    renderWithQuery(<CardImportView card={CARD} accounts={ACCOUNTS} importId="i1" onClose={() => {}}/>);
    const row = (await screen.findByText(/LINHA 1/)).closest("li") as HTMLElement;
    expect(within(row).getByRole("button", {name: "Criar compra"})).toBeDisabled();
    await pick("Categoria para SUPERMERCADO", "Mercado", row);
    await waitFor(() => expect(s.put).toHaveBeenCalledWith(expect.anything(), "inter", {SUPERMERCADO: "food"}, expect.any(String)));
  });

  it("creates the purchase when the category is already mapped", async () => {
    const s = serve(detail([line(1)]), {SUPERMERCADO: "food"});
    renderWithQuery(<CardImportView card={CARD} accounts={ACCOUNTS} importId="i1" onClose={() => {}}/>);
    const row = (await screen.findByText(/LINHA 1/)).closest("li") as HTMLElement;
    await waitFor(() => expect(within(row).getByRole("button", {name: "Criar compra"})).toBeEnabled());
    await userEvent.click(within(row).getByRole("button", {name: "Criar compra"}));
    await waitFor(() => expect(s.create).toHaveBeenCalledWith(expect.anything(), "i1", 1, {category_id: "food"}, expect.any(String)));
  });

  it("calls a refund line a credit and ignores the payment line", async () => {
    const s = serve(detail([line(1, {amount: -500, description: "IFD"}), line(2, {amount: -505257, description: "PAGAMENTO ON LINE", payment: true})], [
      {kind: "ignore", lines: [2]},
    ]), {SUPERMERCADO: "food"});
    renderWithQuery(<CardImportView card={CARD} accounts={ACCOUNTS} importId="i1" onClose={() => {}}/>);
    const refund = (await screen.findByText(/IFD/)).closest("li") as HTMLElement;
    expect(within(refund).getByRole("button", {name: "Criar crédito"})).toBeInTheDocument();
    const payment = screen.getByText(/PAGAMENTO ON LINE/).closest("li") as HTMLElement;
    expect(within(payment).queryByRole("button", {name: /Criar/})).not.toBeInTheDocument();
    await userEvent.click(within(payment).getAllByRole("button", {name: "Ignorar"})[0]);
    expect(s.ignore).not.toHaveBeenCalled(); // asks first
    await userEvent.click(within(payment).getByRole("button", {name: "Voltar"}));
    await userEvent.click(within(payment).getAllByRole("button", {name: "Ignorar"})[0]);
    await userEvent.click(within(payment).getAllByRole("button", {name: "Ignorar"}).at(-1)!);
    await waitFor(() => expect(s.ignore).toHaveBeenCalledWith(expect.anything(), "i1", 2, expect.any(String)));
  });

  it("warns, without blocking, when the statement total differs from the file's", async () => {
    serve(detail([line(1, {status: "linked"}), line(2, {amount: 2000, status: "created"}), line(3, {amount: -505257, status: "ignored", payment: true})]));
    renderWithQuery(<CardImportView card={CARD} accounts={ACCOUNTS} importId="i1" onClose={() => {}}/>);
    const note = await screen.findByRole("status", {name: /Totais/});
    expect(note).toHaveTextContent("R$ 110,51"); // the file without the ignored payment
    expect(note).toHaveTextContent("R$ 90,51"); // the statement
    expect(note).toHaveTextContent(/diferem/);
  });

  it("lets a review be resumed after the drawer was closed", async () => {
    serve(detail([line(1)]), {}, [
      {id: "i1", account_id: "inter", format: "inter_csv" as never, kind: "card", statement_month: "2026-12", created_at: "2026-11-02T10:00:00Z", lines: 5, duplicates: 0, rejected_count: 0, rejected: [], pending: 3},
      {id: "i0", account_id: "inter", format: "inter_csv" as never, kind: "card", statement_month: "2026-11", created_at: "2026-10-02T10:00:00Z", lines: 5, duplicates: 0, rejected_count: 0, rejected: [], pending: 2},
    ]);
    renderWithQuery(<CardImportView card={CARD} accounts={ACCOUNTS} onClose={() => {}}/>);
    await userEvent.click(await screen.findByRole("button", {name: "Continuar revisão (3 pendentes)"}));
    expect(await screen.findByText(/LINHA 1/)).toBeInTheDocument();
    expect(screen.queryByRole("button", {name: /Continuar revisão/})).not.toBeInTheDocument();
  });

  it("lets the person say a suggestion is not that purchase, and confirm-all then leaves it out", async () => {
    const s = serve(detail([line(1), line(2, {amount: 2000, description: "POSTO"})], [
      {kind: "pair", lines: [1], item}, {kind: "pair", lines: [2], item: {...item, purchase_id: "p2", amount: 2000, description: "Gasolina"}},
    ]), {SUPERMERCADO: "food"});
    renderWithQuery(<CardImportView card={CARD} accounts={ACCOUNTS} importId="i1" onClose={() => {}}/>);
    const first = (await screen.findByText("Mercado do bairro")).closest("li") as HTMLElement;
    await userEvent.click(within(first).getByRole("button", {name: "Não é essa"}));
    const row = (await screen.findByText(/LINHA 1/)).closest("li") as HTMLElement;
    expect(within(row).getByRole("button", {name: "Criar compra"})).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", {name: "Confirmar todos (1)"}));
    await userEvent.click(screen.getAllByRole("button", {name: "Confirmar"}).at(-1)!);
    await waitFor(() => expect(s.confirmAll).toHaveBeenCalledWith(expect.anything(), "i1", [1], expect.any(String)));
  });

  it("renders a pair taken by another line in words", async () => {
    const s = serve(detail([line(1)], [{kind: "pair", lines: [1], item}]));
    s.confirm.mockRejectedValue({isAxiosError: true, response: {status: 409, data: {status: 409, code: "pair_taken", title: "x"}}});
    renderWithQuery(<CardImportView card={CARD} accounts={ACCOUNTS} importId="i1" onClose={() => {}}/>);
    await userEvent.click(await screen.findByRole("button", {name: "Confirmar"}));
    expect(await screen.findByRole("alert")).toHaveTextContent(/já está pareada/);
  });
});

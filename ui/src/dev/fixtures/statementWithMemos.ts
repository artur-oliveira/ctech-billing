import type {Statement} from "@/lib/api/financeTypes"

/**
 * A real statement from production (UX batch 4): an opening balance and two
 * settlements with memos, the one that broke Extrato on a phone. Kept verbatim
 * so the mock scenario `extrato_memos` and the tests see exactly what the API
 * answered.
 */
export const STATEMENT_WITH_MEMOS: Statement = {
  account_id: "01M4F5EQ6V4YGJJPT1PQ7HYJBG",
  from: "2026-10-01",
  to: "2026-11-01",
  opening: 0,
  closing: 890031,
  entries: [
    {transaction_id: "01M4F5ER7HSTJF0P9GVVF68B0R", date: "2026-10-08", amount: 872900, balance: 872900, kind: "opening_balance", memo: "Saldo inicial", reversal: false, reversed: false},
    {transaction_id: "01M4G85DJGZJ916DQEGFM61C40", date: "2026-10-09", amount: 21000, balance: 893900, kind: "settlement", memo: "Pix mãe", category_id: "cat-outras-receitas", bill_id: "9BF3F40E4FB62F1717FFD95E7D", reversal: false, reversed: false},
    {transaction_id: "01M4G88QDQBXSCRP05BKAXV74Y", date: "2026-10-09", amount: -3869, balance: 890031, kind: "settlement", memo: "Transferência Mikael", category_id: "cat-lazer", bill_id: "CC4BF28466E2CE6B255FEF110A", reversal: false, reversed: false},
  ],
}

/** The account and the two categories the statement names. */
export const STATEMENT_WITH_MEMOS_ACCOUNTS = [
  {id: "01M4F5EQ6V4YGJJPT1PQ7HYJBG", name: "Conta corrente Itaú", class: "asset" as const, system: false, archived: false, balance: 890031},
  {id: "cat-outras-receitas", name: "Outras receitas", class: "income" as const, dre_group: "gross_revenue" as const, system: false, archived: false, balance: 0},
  {id: "cat-lazer", name: "Lazer", class: "expense" as const, dre_group: "operating_expenses" as const, system: false, archived: false, balance: 0},
]

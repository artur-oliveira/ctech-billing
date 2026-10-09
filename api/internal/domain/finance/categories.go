package finance

// DefaultCategories is the chart a new space starts with (spec § 3.3): one set
// for a person, one for an organization, each with the two categories a
// settlement for a different amount needs (§ 3.2). Ids are fixed so seeding is
// idempotent; the person renames, archives or adds to them like any other.
func DefaultCategories(personal bool) []LedgerAccount {
	in := func(id, name string, g DREGroup) LedgerAccount {
		return LedgerAccount{ID: "cat-" + id, Name: name, Class: ClassIncome, Group: g}
	}
	out := func(id, name string, g DREGroup) LedgerAccount {
		return LedgerAccount{ID: "cat-" + id, Name: name, Class: ClassExpense, Group: g}
	}
	gaps := []LedgerAccount{
		out("juros-multas", "Juros e multas", GroupFinancialResult),
		in("descontos-obtidos", "Descontos obtidos", GroupFinancialResult),
	}
	if personal {
		return append([]LedgerAccount{
			in("salario", "Salário", GroupGrossRevenue),
			in("rendimentos", "Rendimentos", GroupFinancialResult),
			in("outras-receitas", "Outras receitas", GroupOther),
			out("moradia", "Moradia", GroupOperatingExpenses),
			out("alimentacao", "Alimentação", GroupOperatingExpenses),
			out("transporte", "Transporte", GroupOperatingExpenses),
			out("saude", "Saúde", GroupOperatingExpenses),
			out("educacao", "Educação", GroupOperatingExpenses),
			out("lazer", "Lazer", GroupOperatingExpenses),
			out("assinaturas", "Assinaturas e serviços", GroupOperatingExpenses),
			out("impostos-taxas", "Impostos e taxas", GroupOperatingExpenses),
			out("outras-despesas", "Outras despesas", GroupOther),
		}, gaps...)
	}
	return append([]LedgerAccount{
		in("vendas", "Vendas", GroupGrossRevenue),
		in("servicos", "Prestação de serviços", GroupGrossRevenue),
		in("rendimentos", "Rendimentos", GroupFinancialResult),
		out("impostos-vendas", "Impostos sobre vendas", GroupDeductions),
		out("custo-vendas", "Custo das vendas e serviços", GroupCosts),
		out("pessoal", "Salários e encargos", GroupOperatingExpenses),
		out("aluguel", "Aluguel", GroupOperatingExpenses),
		out("software", "Software e assinaturas", GroupOperatingExpenses),
		out("marketing", "Marketing", GroupOperatingExpenses),
		out("administrativas", "Despesas administrativas", GroupOperatingExpenses),
		out("tarifas-bancarias", "Tarifas bancárias", GroupFinancialResult),
		out("outras-despesas", "Outras despesas", GroupOther),
	}, gaps...)
}

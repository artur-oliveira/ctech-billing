package finance

// DefaultCategories is the chart a new space starts with (spec § 3.3): one set
// for a person, one for an organization, each with the two categories a
// settlement for a different amount needs (§ 3.2). Ids are fixed so seeding is
// idempotent; the person renames, archives or adds to them like any other.
func DefaultCategories(personal bool) []LedgerAccount {
	in := func(id, name string, g DREGroup) LedgerAccount {
		return LedgerAccount{ID: "cat-" + id, Name: name, Class: ClassIncome, Group: g, SystemKey: systemKey(id)}
	}
	out := func(id, name string, g DREGroup) LedgerAccount {
		return LedgerAccount{ID: "cat-" + id, Name: name, Class: ClassExpense, Group: g, SystemKey: systemKey(id)}
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
			BillingCategory(Payable),
			out("impostos-taxas", "Impostos e taxas", GroupOperatingExpenses),
			out("outras-despesas", "Outras despesas", GroupOther),
		}, gaps...)
	}
	return append([]LedgerAccount{
		in("vendas", "Vendas", GroupGrossRevenue),
		in("servicos", "Prestação de serviços", GroupGrossRevenue),
		BillingCategory(Receivable),
		in("rendimentos", "Rendimentos", GroupFinancialResult),
		out("impostos-vendas", "Impostos sobre vendas", GroupDeductions),
		out("custo-vendas", "Custo das vendas e serviços", GroupCosts),
		out("pessoal", "Salários e encargos", GroupOperatingExpenses),
		out("aluguel", "Aluguel", GroupOperatingExpenses),
		out("software", "Software e assinaturas", GroupOperatingExpenses),
		BillingCategory(Payable),
		out("marketing", "Marketing", GroupOperatingExpenses),
		out("administrativas", "Despesas administrativas", GroupOperatingExpenses),
		out("tarifas-bancarias", "Tarifas bancárias", GroupFinancialResult),
		out("outras-despesas", "Outras despesas", GroupOther),
	}, gaps...)
}

// categoryKeys maps a default category id (without the "cat-" prefix) to its
// stable system key. "rendimentos" and "outras-despesas" are shared by both sets.
var categoryKeys = map[string]string{
	"juros-multas":        "interest_and_fines",
	"descontos-obtidos":   "discounts_obtained",
	"salario":             "salary",
	"rendimentos":         "investment_income",
	"outras-receitas":     "other_income",
	"moradia":             "housing",
	"alimentacao":         "food",
	"transporte":          "transport",
	"saude":               "health",
	"educacao":            "education",
	"lazer":               "leisure",
	"assinaturas":         "subscriptions_services",
	"impostos-taxas":      "taxes_and_fees",
	"outras-despesas":     "other_expenses",
	"vendas":              "sales",
	"servicos":            "services_revenue",
	"impostos-vendas":     "sales_taxes",
	"custo-vendas":        "cost_of_sales",
	"pessoal":             "payroll_and_charges",
	"aluguel":             "rent",
	"software":            "software_subscriptions",
	"marketing":           "marketing",
	"administrativas":     "administrative_expenses",
	"tarifas-bancarias":   "bank_fees",
	"assinaturas-receita": "subscriptions_revenue",
	"assinaturas-ctech":   "ctech_subscriptions",
}

func systemKey(id string) string { return categoryKeys[id] }

// The categories billing's own invoices post under (spec § 3.8): what an
// organization's paid invoices earn, and what a space pays CTech. Seeded with
// every space; the posting rule also ensures them by id, so a space that
// predates them, or whose person already used the name, still gets its bill.
const (
	CategorySubscriptionRevenue = "cat-assinaturas-receita"
	CategoryCTechSubscriptions  = "cat-assinaturas-ctech"
)

// BillingCategory is the category a paid invoice posts under, by the side of the
// invoice a space is on: revenue for the issuer, an expense for the payer. The
// seeded defaults are built from it, so the two can never disagree.
func BillingCategory(dir Direction) LedgerAccount {
	if dir == Receivable {
		return LedgerAccount{ID: CategorySubscriptionRevenue, Name: "Assinaturas", Class: ClassIncome,
			Group: GroupGrossRevenue, SystemKey: systemKey("assinaturas-receita")}
	}
	return LedgerAccount{ID: CategoryCTechSubscriptions, Name: "Assinaturas CTech", Class: ClassExpense,
		Group: GroupOperatingExpenses, SystemKey: systemKey("assinaturas-ctech")}
}

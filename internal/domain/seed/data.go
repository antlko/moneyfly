// Package seed holds the canonical taxonomy every new user starts with, and the
// code that installs it.
//
// Categories, accounts and aliases are user-scoped rows, so they cannot be
// INSERTed by a migration — at migration time there is no user to own them. They
// are installed when a user is created instead. See the note in
// docs/03-data-model.md §3.7.
//
// The alias table below is the reason the stage-04 import gate passes with no
// manual mapping: every name observed in the real export resolves. A missing
// alias here is a bug here, not there (docs/04-import-monefy.md §4.7).
package seed

import (
	"github.com/antlko/moneyapp/internal/domain/account"
	"github.com/antlko/moneyapp/internal/domain/category"
)

// CategorySeed is one canonical category plus the source names that map to it.
type CategorySeed struct {
	Name        string
	Kind        category.Kind
	IsEssential bool
	Icon        string
	Color       string
	// PlannedMinor is the workbook's column B figure in EUR minor units. It is
	// reference data for the budget bulk-seed screen, not a budget row: budgets
	// are per month and are written through POST /budgets/bulk.
	PlannedMinor int64
	// Aliases are source names from the observed exports. The canonical name is
	// always included as an alias by Categories(), so an exact match resolves
	// without a fuzzy proposal.
	Aliases []string
}

// Categories returns the 20 canonical categories in workbook order.
//
// Eighteen come from rows 9-26 of the workbook, with the misspellings corrected
// (`Applience` -> `Appliances`, `Toilery` -> `Toiletry`) and retained as aliases.
// `Utilities` and `Taxi` are new: they had no spreadsheet row at all, which is
// where 120 of the lost transactions went.
//
// is_essential is exactly the membership of the workbook's B28 formula — the 13
// categories that make up Possible Minimum.
func Categories() []CategorySeed {
	return []CategorySeed{
		{Name: "House", Kind: category.KindExpense, IsEssential: true, Icon: "🏠", Color: "#4F7CAC", PlannedMinor: 75000},
		{Name: "Appliances", Kind: category.KindExpense, Icon: "🔌", Color: "#8E7DBE", PlannedMinor: 15000,
			Aliases: []string{"Applience", "Appliance"}},
		{Name: "Hotel/Trip", Kind: category.KindExpense, Icon: "✈️", Color: "#4FB0C6", PlannedMinor: 25000,
			Aliases: []string{"HotelTrip", "Hotel Trip"}},
		{Name: "Food", Kind: category.KindExpense, IsEssential: true, Icon: "🛒", Color: "#57BB8A", PlannedMinor: 30000},
		{Name: "Eating out", Kind: category.KindExpense, IsEssential: true, Icon: "🍽️", Color: "#E8A33D", PlannedMinor: 10000,
			Aliases: []string{"Eating Out", "Restaurants"}},
		{Name: "Family", Kind: category.KindExpense, Icon: "👨‍👩‍👧", Color: "#D96C6C", PlannedMinor: 6000,
			// The trailing space is real: it appears in the export and must map
			// independently, because raw text drives the natural key.
			Aliases: []string{"Family "}},
		{Name: "Gifts", Kind: category.KindExpense, Icon: "🎁", Color: "#C77DBB", PlannedMinor: 8000},
		{Name: "Entertainment", Kind: category.KindExpense, Icon: "🎬", Color: "#7D8CC4", PlannedMinor: 3000},
		{Name: "Toiletry", Kind: category.KindExpense, IsEssential: true, Icon: "🧼", Color: "#6BB7B7", PlannedMinor: 1500,
			Aliases: []string{"Toilery"}},
		{Name: "Studying", Kind: category.KindExpense, IsEssential: true, Icon: "📚", Color: "#5E8B7E", PlannedMinor: 1500,
			Aliases: []string{"Studing", "Studying "}},
		{Name: "Hobby", Kind: category.KindExpense, IsEssential: true, Icon: "🎨", Color: "#B5838D", PlannedMinor: 2000},
		{Name: "Clothes", Kind: category.KindExpense, IsEssential: true, Icon: "👕", Color: "#9A8C98", PlannedMinor: 3000,
			Aliases: []string{"Clouth", "Clouths", "Clothing"}},
		{Name: "Transport", Kind: category.KindExpense, IsEssential: true, Icon: "🚌", Color: "#4E8098", PlannedMinor: 15000},
		{Name: "Health", Kind: category.KindExpense, IsEssential: true, Icon: "💊", Color: "#DE6B72", PlannedMinor: 22000},
		{Name: "Communications", Kind: category.KindExpense, IsEssential: true, Icon: "📱", Color: "#5FA8D3", PlannedMinor: 2000,
			Aliases: []string{"Communication"}},
		{Name: "Sport", Kind: category.KindExpense, IsEssential: true, Icon: "🏋️", Color: "#68A357", PlannedMinor: 3000,
			Aliases: []string{"Sports"}},
		{Name: "Bills", Kind: category.KindExpense, IsEssential: true, Icon: "🧾", Color: "#A67B5B", PlannedMinor: 3000,
			// Monefy exports in the device locale; the older exports are Russian.
			Aliases: []string{"Счета"}},
		{Name: "Services", Kind: category.KindExpense, IsEssential: true, Icon: "🛠️", Color: "#7E8D85", PlannedMinor: 10000},
		// New categories. 119 `Utilities` rows and 1 `Taxi` row had nowhere to go
		// in the old pipeline and were silently dropped. Folding them into Bills
		// and Transport instead is a one-line alias change.
		{Name: "Utilities", Kind: category.KindExpense, Icon: "💡", Color: "#C9A227"},
		{Name: "Taxi", Kind: category.KindExpense, Icon: "🚕", Color: "#E0B34F"},

		// Income. Monefy exports none — 0 of 1,683 rows are positive — and the
		// workbook typed it into the formula bar as `=3186+183+200+86`, which is
		// why row 31 has no line items behind it (docs/04-import-monefy.md §4.8).
		// Without at least one income category there is nothing to file a salary
		// against, and Diff and Saved % cannot be computed at all. `Salary` is
		// workbook row 31; its 2800 is the planned figure there.
		{Name: "Salary", Kind: category.KindIncome, Icon: "💼", Color: "#3F8F5B", PlannedMinor: 280000},
		{Name: "Other income", Kind: category.KindIncome, Icon: "🪙", Color: "#6BA292",
			Aliases: []string{"Bonus", "Refund"}},
	}
}

// AccountSeed is one canonical account.
type AccountSeed struct {
	Name     string
	Class    account.AssetClass
	Currency string
	IsLiquid bool
	Counts   bool
	Ticker   string
	// Parent names a seeded account this one rolls up into. A parent holds no
	// value of its own; it is a SUM over its children at query time.
	Parent  string
	Aliases []string
}

// Accounts returns the chart of accounts from workbook rows 36-69
// (docs/appendix-excel-parity.md §A.4).
//
// `Cash` and `Banks` were computed parents in the sheet, enumerating their
// children by hand — which is how `Banks FOP` came to be excluded from `Banks`
// while being included in `General`. Here they are real parent rows whose value
// is a sum, so a child can never be forgotten.
//
// `Cash UAH` has no workbook row: it exists because the Russian-locale exports
// post to `Наличные`, which is a UAH cash account.
func Accounts() []AccountSeed {
	return []AccountSeed{
		{Name: "Gold", Class: account.ClassMetal, Currency: "EUR", IsLiquid: false, Counts: true, Ticker: "XAU"},

		{Name: "Cash", Class: account.ClassCash, Currency: "EUR", IsLiquid: true, Counts: true},
		{Name: "Cash USD", Class: account.ClassCash, Currency: "USD", IsLiquid: true, Counts: true, Parent: "Cash"},
		{Name: "Cash EUR", Class: account.ClassCash, Currency: "EUR", IsLiquid: true, Counts: true, Parent: "Cash",
			Aliases: []string{"EUR"}},
		{Name: "Cash HUF", Class: account.ClassCash, Currency: "HUF", IsLiquid: true, Counts: true, Parent: "Cash",
			Aliases: []string{"HUF"}},
		{Name: "Cash UAH", Class: account.ClassCash, Currency: "UAH", IsLiquid: true, Counts: true, Parent: "Cash",
			// Monefy names accounts after currencies, so the export's `UAH`
			// account is this one; `Наличные` is the same account in Russian.
			Aliases: []string{"UAH", "Наличные"}},

		{Name: "Banks", Class: account.ClassBank, Currency: "EUR", IsLiquid: true, Counts: true},
		{Name: "Banks USD", Class: account.ClassBank, Currency: "USD", IsLiquid: true, Counts: true, Parent: "Banks"},
		{Name: "Banks EUR", Class: account.ClassBank, Currency: "EUR", IsLiquid: true, Counts: true, Parent: "Banks"},
		{Name: "Banks HUF", Class: account.ClassBank, Currency: "HUF", IsLiquid: true, Counts: true, Parent: "Banks"},
		{Name: "Banks UAH", Class: account.ClassBank, Currency: "UAH", IsLiquid: true, Counts: true, Parent: "Banks"},
		// FOP is a Ukrainian sole-trader account, hence UAH.
		{Name: "Banks FOP", Class: account.ClassBank, Currency: "UAH", IsLiquid: true, Counts: true, Parent: "Banks"},

		{Name: "Deposits", Class: account.ClassDeposit, Currency: "EUR", IsLiquid: false, Counts: true},
		{Name: "Deposits USD", Class: account.ClassDeposit, Currency: "USD", IsLiquid: false, Counts: true, Parent: "Deposits"},
		{Name: "Deposits EUR", Class: account.ClassDeposit, Currency: "EUR", IsLiquid: false, Counts: true, Parent: "Deposits"},

		{Name: "Invests", Class: account.ClassInvestment, Currency: "EUR", IsLiquid: false, Counts: true},
		{Name: "CSGO Skins", Class: account.ClassOther, Currency: "EUR", IsLiquid: false, Counts: true},
		{Name: "Ton", Class: account.ClassCrypto, Currency: "EUR", IsLiquid: false, Counts: true},
		{Name: "USDT (EUR)", Class: account.ClassCrypto, Currency: "EUR", IsLiquid: false, Counts: true, Ticker: "USDT"},
	}
}

// EssentialCount is the number of categories in the workbook's Possible Minimum.
const EssentialCount = 13

// CategoryCount is the number of seeded categories: 20 expense plus 2 income.
const CategoryCount = 22

// ExpenseCategoryCount is the expense half — the workbook's 18 rows plus
// Utilities and Taxi, which had no row at all.
const ExpenseCategoryCount = 20

// IncomeCategoryCount is the income half, added in stage 05 so Diff and Saved %
// have line items to work from.
const IncomeCategoryCount = 2

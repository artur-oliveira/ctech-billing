# Product

## Register

product

## Platform

web

## Users

Everyone who signs in is a **CTech account holder**. What differs is what they hold, and the same
person routinely holds both — which is why this is two route shells rather than two apps, and why
neither is a "role" the user selects.

**Everyone is a customer.** They buy CTech products and CTech invoices them; billing's tenant zero
is CTech itself ([ADR 0001](../docs/adr/0001-product-scope-a-and-b.md)). The portal answers their three
questions — what am I paying, how much, when — and opens on *my subscriptions*. It must never show
a merchant's internal vocabulary: no status enum, no metadata, no audit trail, no error code.

**Some are also operators.** A customer whose account has been provisioned an organization can
invoice customers of their own. That is what the console is for, and it is additive: the same person
sees *my invoices* and *my billing* without changing accounts or toggling a mode. They live in
tables — subscriptions, invoices, customers, catalogue — and open the console to answer *who is
active*. They already know what a price version and a proration are, so the console must show those
things rather than explain them.

**A third-party merchant's own customers are not users of this product.** They never sign in here.
They reach a single invoice through the public hosted checkout, by a link, and that page is the
whole of their experience. A merchant who wants more than that builds it themselves against the M2M
API. This is a boundary, not a gap: billing does not ship a portal for other people's customers.

## Product Purpose

Billing turns a subscription into money collected, and answers "is this customer entitled?" for
every other CTech product. Success is a merchant who never opens a spreadsheet to know who owes
what, and a consumer who never emails support to ask what a charge was for.

## Positioning

The billing system that shows its work: every amount on the screen can be traced to the price, the
period, and the person who changed it.

## Brand Personality

A product that is paid for and looks it. Present, not invisible: the interface has a point of view,
it is confident about what matters on each screen, and it is unmistakably CTech without being
mistakable for ctech-dfe. The voice is plain Brazilian Portuguese — precise on the console side,
human on the consumer side. Never playful about money, never bureaucratic about it either.

Billing carries **its own identity on shared components**. The button, table, modal and field
vocabulary is the one ctech-dfe already established; the color, the density and the tone are
billing's own. A user should recognize the family and still know they changed products.

## Anti-references

- **Generic SaaS purple-and-gradient.** Gradient headings, repeated icon cards, a giant metric at
  the top of every page. The look every new product ships with.
- **Dense ERP with no hierarchy.** Everything the same size, everything equally important, screens
  built out of fields.
- **Playful consumer fintech.** Illustrations, emoji, a light tone. Money treated as a game — which
  on the consumer side is the tempting mistake, and the wrong one.

## Design Principles

- **Two shells, one system, one account.** The console is dense because an operator works in it all
  day; the portal is spare because the same person visits it twice a month wearing the other hat.
  Same tokens and same components, deliberately different densities. A console component appearing
  in the portal is a bug — and so is asking the user which one they are.
- **The tenant is never a question on screen.** Nothing in the UI lets anyone type, pick, or guess
  an organization; test-versus-live is the one mode the operator switches, and it is always visible
  because acting on the wrong one is the expensive mistake.
- **Show the derivation, not just the number.** Every total can be opened into its lines, its
  period, and its history. This is what "shows its work" means in practice, and it is why detail
  screens carry a timeline.
- **The consumer never reads an internal name.** Status, cause and error codes are translated at the
  edge, always. "Vence em 3 dias", never `OPEN`.
- **Teach immutability instead of hiding it.** A price cannot be edited, only replaced; a canceled
  subscription keeps its invoices. The UI should make those rules obvious rather than paper over
  them with an edit button that creates something new behind the user's back.

## Accessibility & Inclusion

WCAG 2.2 AA as the floor, verified rather than promised: body text at ≥4.5:1, visible focus on every
interactive element, and status never signalled by color alone — a badge carries a glyph or a word
as well as a hue.

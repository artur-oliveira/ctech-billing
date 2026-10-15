import type {QueryKey} from "@tanstack/react-query";

import {checkoutKeys} from "@/lib/api/checkout";
import {connectionKeys} from "@/lib/api/connections";
import {consoleKeys} from "@/lib/api/console";
import {financeKeys} from "@/lib/api/finance";
import {portalKeys} from "@/lib/api/portal";
import type {ConsoleScope} from "@/lib/console/merchant";
import type {Mode} from "@/lib/console/mode";
import type {Space} from "@/lib/console/space";

import type {ChangeView} from "./codec";

/** The principal a provider is mounted for. It decides which cache a change touches. */
export type Surface =
  | {kind: "console"; scope: ConsoleScope}
  | {kind: "portal"}
  | {kind: "finance"; mode: Mode; space: Space}
  | {kind: "checkout"; token: string}

/**
 * What a change touches: the query keys to invalidate, as prefixes (TanStack
 * invalidates every query whose key starts with one).
 *
 * A change is a hint to read (spec D3), so this only ever invalidates: it never
 * writes a value into the cache. The cached views carry fields the server derives
 * (a state next to its tone, an amount due) and a client that patched a status
 * word would be inventing the rest. The active query refetches within a request.
 *
 * This table is the specification of what updates what:
 *
 * | entity                     | console                  | portal            | finance            | checkout     |
 * | invoice                    | invoices, overview       | invoices          | none               | the view     |
 * | invoice.payer_recorded     | that invoice             | that invoice      | none               | the view     |
 * | payment, checkout_session  | invoices                 | invoices          | none               | the view     |
 * | credit_note                | invoices                 | invoices          | none               | the view     |
 * | subscription               | subscriptions, overview  | subscriptions     | none               | none         |
 * | customer                   | customers                | none              | none               | none         |
 * | payout, connection         | the connections          | none              | connections, accts | none         |
 * | statement, bill            | none                     | none              | the whole space    | none         |
 *
 * A known entity with a type this client does not know invalidates that entity's
 * prefix (never nothing, never everything). An entity it does not know
 * invalidates nothing: it cannot tell which screen shows it, and the screens
 * still refetch on focus.
 */
export function effectsOf(surface: Surface, c: ChangeView): {invalidate: QueryKey[]} {
  switch (surface.kind) {
    case "console":
      return {invalidate: consoleEffects(surface.scope, c)};
    case "portal":
      return {invalidate: portalEffects(c)};
    case "finance":
      return {invalidate: financeEffects(surface.mode, surface.space, c)};
    case "checkout":
      return {invalidate: checkoutEffects(surface.token, c)};
    default:
      return {invalidate: []}; // a surface kind this build does not know
  }
}

/** ["console", org, mode]: the root of everything the console caches for a scope. */
function consoleRoot(scope: ConsoleScope): QueryKey {
  return consoleKeys.settings(scope).slice(0, 3);
}

function consoleEffects(scope: ConsoleScope, c: ChangeView): QueryKey[] {
  const overview: QueryKey = [...consoleRoot(scope), "overview"];
  switch (c.entity) {
    case "invoice":
      return c.type === "invoice.payer_recorded"
        ? [consoleKeys.invoice(scope, c.entityId)]
        : [consoleKeys.invoices(scope), overview];
    case "payment":
    case "checkout_session":
    case "credit_note":
      // These changes name the payment, the session or the note, not the invoice,
      // so the invoices' prefix is invalidated (only the active ones refetch).
      return [consoleKeys.invoices(scope)];
    case "subscription":
      return [consoleKeys.subscriptions(scope), overview];
    case "customer":
      return [consoleKeys.customers(scope)];
    case "payout":
    case "connection":
      return [connectionKeys.list({org: scope.org, mode: scope.mode})];
    default:
      return [];
  }
}

function portalEffects(c: ChangeView): QueryKey[] {
  switch (c.entity) {
    case "invoice":
      return c.type === "invoice.payer_recorded" ? [portalKeys.invoice(c.entityId)] : [portalKeys.invoices];
    case "payment":
    case "checkout_session":
    case "credit_note":
      return [portalKeys.invoices];
    case "subscription":
      return [portalKeys.subscriptions];
    default:
      return [];
  }
}

function financeEffects(mode: Mode, space: Space, c: ChangeView): QueryKey[] {
  switch (c.entity) {
    case "payout":
    case "connection": {
      const keys: QueryKey[] = [financeKeys.accounts(mode, space)];
      // A connection belongs to an organization; a personal space has none.
      if (space.kind === "organization") keys.unshift(connectionKeys.list({org: space.organizationId, mode}));
      return keys;
    }
    case "statement":
    case "bill":
      // The whole space's prefix: the bills, the statement, the accounts and the
      // unclassified lines all sit under it, and one line posted can move any.
      return [financeKeys.all(mode, space)];
    default:
      return [];
  }
}

function checkoutEffects(token: string, c: ChangeView): QueryKey[] {
  switch (c.entity) {
    case "invoice":
    case "payment":
    case "checkout_session":
    case "credit_note":
      return [checkoutKeys.view(token)];
    default:
      return [];
  }
}

/**
 * Every query a surface holds: what a Gap invalidates, because after one nothing
 * the screen shows can be trusted. Never another surface's.
 */
export function rootsOf(surface: Surface): QueryKey[] {
  switch (surface.kind) {
    case "console":
      return [consoleRoot(surface.scope), connectionKeys.list({org: surface.scope.org, mode: surface.scope.mode})];
    case "portal":
      return [["portal"]];
    case "finance": {
      const roots: QueryKey[] = [financeKeys.all(surface.mode, surface.space)];
      if (surface.space.kind === "organization") {
        roots.push(connectionKeys.list({org: surface.space.organizationId, mode: surface.mode}));
      }
      return roots;
    }
    case "checkout":
      return [checkoutKeys.view(surface.token)];
    default:
      return [];
  }
}

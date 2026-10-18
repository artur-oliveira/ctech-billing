"use client";

import {useQueryClient} from "@tanstack/react-query";
import {useState, useSyncExternalStore} from "react";

import {installRealtimeMock} from "@/dev/realtimeMock";
import {currentScenario, setScenario} from "@/dev/mockRuntime";
import {DEFAULT_SCENARIO, MOCK_SCENARIOS, USE_MOCK, type MockScenario} from "@/lib/mockConfig";

// The socket must be replaced before any provider connects, so at import, not in an effect.
if (USE_MOCK) installRealtimeMock();

/**
 * The scenario lives in localStorage, which the server cannot see.
 * `useSyncExternalStore` is what that shape is for: it takes a separate server
 * snapshot, so the markup React renders on the server and the markup it
 * hydrates agree instead of throwing a mismatch on every load.
 */
function useScenario(): MockScenario {
  return useSyncExternalStore(
    onChange => {
      window.addEventListener("ctech-mock-scenario", onChange);
      return () => window.removeEventListener("ctech-mock-scenario", onChange);
    },
    currentScenario,
    () => DEFAULT_SCENARIO
  );
}

/**
 * The scenario switcher, visible only in mock mode.
 *
 * It sits over the interface on purpose. Every state in the list is one this
 * portal has to render correctly and most of them cannot be reached against a
 * real backend without waiting days, so the ones that get designed are the
 * ones that are one click away.
 */
export function MockControls() {
  const queryClient = useQueryClient();
  const scenario = useScenario();
  const [open, setOpen] = useState(false);

  if (!USE_MOCK) return null;

  function choose(next: MockScenario) {
    setScenario(next);
    setOpen(false);
    void queryClient.invalidateQueries();
  }

  return (
    // Docked to the left edge, small, above the phone's bottom bar: never over a tab or a page's content.
    <div className="fixed bottom-2 left-0 z-[60] max-md:bottom-[calc(4.75rem+env(safe-area-inset-bottom))] print:hidden">
      {open && (
        <ul className="mb-2 w-64 overflow-hidden rounded-lg border border-border bg-background shadow-modal">
          {MOCK_SCENARIOS.map(s => (
            <li key={s.id}>
              <button
                type="button"
                onClick={() => choose(s.id)}
                className={`flex w-full items-center justify-between px-3 py-2 text-left text-sm transition-colors hover:bg-surface ${
                  s.id === scenario ? "font-medium text-brand-600" : "text-foreground"
                }`}
              >
                {s.label}
                {s.id === scenario && <span aria-hidden>•</span>}
              </button>
            </li>
          ))}
        </ul>
      )}
      <button
        type="button"
        onClick={() => setOpen(o => !o)}
        aria-expanded={open}
        title={`mock · ${scenario}`}
        className="rounded-r-full border border-l-0 border-border bg-background/80 py-0.5 pr-2 pl-1.5 font-mono text-[10px] text-muted-foreground opacity-60 shadow-card transition hover:text-foreground hover:opacity-100 focus-visible:opacity-100 max-md:max-w-12 max-md:truncate"
      >
        mock · {scenario}
      </button>
    </div>
  );
}

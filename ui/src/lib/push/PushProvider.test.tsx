import "@testing-library/jest-dom/vitest";

import {act, cleanup, render} from "@testing-library/react";
import {afterEach, beforeEach, describe, expect, it, vi} from "vitest";

const mocks = vi.hoisted(() => ({
  push: vi.fn(),
  register: vi.fn().mockResolvedValue(null),
  subscription: vi.fn(),
  stampWhenSyncRan: vi.fn(),
  handler: null as null | ((path: string) => void),
  subject: "u1" as string | null,
}));

vi.mock("next/navigation", () => ({useRouter: () => ({push: mocks.push})}));
vi.mock("@/lib/auth/subject", () => ({useSubject: () => mocks.subject}));
vi.mock("@aoctech/push-client", async () => {
  const {useEffect} = await import("react");
  return {
    registerServiceWorker: mocks.register,
    onNavigateMessage: (handler: (path: string) => void) => {
      mocks.handler = handler;
      return () => {
        mocks.handler = null;
      };
    },
    // Like the real hook, the "sync" happens in an effect once `enabled` is true; it reads the stamp the way the client does.
    usePushSubscription: (options: {enabled: boolean}) => {
      mocks.subscription(options);
      useEffect(() => {
        if (options.enabled) mocks.stampWhenSyncRan(localStorage.getItem("ctech-push:last-sync"));
      }, [options.enabled]);
      return {status: "default", subscribe: vi.fn(), unsubscribe: vi.fn()};
    },
  };
});

import {PushProvider, usePush} from "@/lib/push/PushProvider";
import {PUSH_STAMP_KEY, PUSH_USER_KEY} from "@/lib/push/storage";

function Probe() {
  const {status, canInstall} = usePush();
  return <p data-testid="probe">{status}/{String(canInstall)}</p>;
}

const renderProvider = () => render(<PushProvider><Probe/></PushProvider>);
const STAMP = '{"at":1763400000000,"kid":"k1"}';

beforeEach(() => {
  localStorage.clear();
  mocks.subject = "u1";
});
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("PushProvider", () => {
  it("registers the service worker once and syncs only with a signed-in person", () => {
    renderProvider();
    expect(mocks.register).toHaveBeenCalledTimes(1);
    expect(mocks.subscription).toHaveBeenLastCalledWith(expect.objectContaining({enabled: true}));
    cleanup();
    mocks.subject = null;
    renderProvider();
    expect(mocks.subscription).toHaveBeenLastCalledWith(expect.objectContaining({enabled: false}));
  });

  it("a click on a notification routes to its link", () => {
    renderProvider();
    act(() => mocks.handler?.("/finance/inbox"));
    expect(mocks.push).toHaveBeenCalledWith("/finance/inbox");
  });

  describe("one browser, one person", () => {
    it("another person signs in on the same browser: the stamp drops so the sync resends", () => {
      localStorage.setItem(PUSH_STAMP_KEY, STAMP);
      localStorage.setItem(PUSH_USER_KEY, "u-before");
      renderProvider();
      expect(localStorage.getItem(PUSH_STAMP_KEY)).toBeNull();
      expect(localStorage.getItem(PUSH_USER_KEY)).toBe("u1");
    });

    it("the same person again: the stamp stays", () => {
      localStorage.setItem(PUSH_STAMP_KEY, STAMP);
      localStorage.setItem(PUSH_USER_KEY, "u1");
      renderProvider();
      expect(localStorage.getItem(PUSH_STAMP_KEY)).toBe(STAMP);
    });

    it("no record of who synced last: resend", () => {
      localStorage.setItem(PUSH_STAMP_KEY, STAMP);
      renderProvider();
      expect(localStorage.getItem(PUSH_STAMP_KEY)).toBeNull();
    });

    it("the stamp is already gone when the sync runs (effect order is what guarantees the resend)", () => {
      localStorage.setItem(PUSH_STAMP_KEY, STAMP);
      localStorage.setItem(PUSH_USER_KEY, "u-before");
      renderProvider();
      expect(mocks.stampWhenSyncRan).toHaveBeenCalledWith(null);
    });
  });

  it("offers the install prompt after beforeinstallprompt", () => {
    const {getByTestId} = renderProvider();
    expect(getByTestId("probe").textContent).toBe("default/false");
    act(() => {
      const event = new Event("beforeinstallprompt", {cancelable: true});
      Object.assign(event, {prompt: vi.fn().mockResolvedValue(undefined)});
      window.dispatchEvent(event);
    });
    expect(getByTestId("probe").textContent).toBe("default/true");
  });
});

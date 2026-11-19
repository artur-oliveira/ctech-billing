import {deletePushDevice} from '@/lib/api/push';
import {pushDeviceId} from '@/lib/push/device';
import {PUSH_STAMP_KEY, PUSH_USER_KEY} from '@/lib/push/storage';

/** Logout must never wait on the network for long: past this the page goes on and leaves the rest to the push service. */
export const PUSH_LOGOUT_TIMEOUT_MS = 2000;

function forgetPushBookkeeping(): void {
  try {
    localStorage.removeItem(PUSH_STAMP_KEY);
    localStorage.removeItem(PUSH_USER_KEY);
  } catch {
    // Storage unavailable: the stamp's own 24 h expiry still makes the next login resend the subscription.
  }
}

/**
 * Stops this browser from notifying the person who is leaving (shared computers). In this order, because logging out
 * must not fail open:
 *   1. forget the sync stamp and the person it was for (synchronous), so the next login resends the subscription;
 *   2. cancel the browser subscription (local and fast): from here the push service answers 410 for it and the sender
 *      drops the server row by itself, even if nothing below succeeds;
 *   3. delete the server row while the token still exists, bounded by `timeoutMs`.
 * Best effort and never throws: logging out does not depend on the network.
 */
export async function removeThisDevicePush(timeoutMs = PUSH_LOGOUT_TIMEOUT_MS): Promise<void> {
  forgetPushBookkeeping();
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return;
  const work = (async () => {
    const registration = await navigator.serviceWorker.getRegistration('/');
    const subscription = await registration?.pushManager.getSubscription();
    if (!subscription) return;
    const id = await pushDeviceId(subscription.endpoint);
    await subscription.unsubscribe().catch(() => false);
    await deletePushDevice(id).catch(() => undefined);
  })().catch(() => undefined);
  await Promise.race([work, new Promise<void>(resolve => setTimeout(resolve, timeoutMs))]);
}

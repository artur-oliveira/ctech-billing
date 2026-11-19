/**
 * The id the API gives a device: 16 hex characters of SHA-256(endpoint) (`pushsvc.SubscriptionID`). Computing it here
 * lets the screen mark "this device" and remove it by id without the server ever returning the endpoint, which is a
 * bearer credential.
 */
export async function pushDeviceId(endpoint: string): Promise<string> {
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(endpoint));
  return Array.from(new Uint8Array(digest).slice(0, 8), b => b.toString(16).padStart(2, '0')).join('');
}

/** The device id of this browser's current subscription, or null when it has none (or push is unsupported). */
export async function currentPushDeviceId(): Promise<string | null> {
  if (typeof navigator === 'undefined' || !('serviceWorker' in navigator)) return null;
  const registration = await navigator.serviceWorker.getRegistration('/');
  const subscription = await registration?.pushManager.getSubscription();
  return subscription ? pushDeviceId(subscription.endpoint) : null;
}

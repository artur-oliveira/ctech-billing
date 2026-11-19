import {apiClient} from "@/lib/api/client";

/**
 * Web Push (go-common pushsvc routes under /v1.0/console/finance/push, and Billing's own preferences). Push is personal:
 * no mode and no space header, the routes answer about the signed-in person and their devices.
 */
const BASE = "/v1.0/console/finance/push";

export interface PushConfig {
  /** False: this installation has no VAPID key; the screen says so and subscribing answers 503. */
  enabled: boolean
  /** VAPID public key, base64url (uncompressed P-256 point). Absent when disabled. */
  public_key?: string
  /** Short key id; the client resubscribes when it changes. */
  vapid_kid?: string
  max_subscriptions: number
}

export interface PushSubscriptionBody {
  endpoint: string
  keys: {p256dh: string; auth: string}
}

export interface PushPreferences {
  pending_reminder: {enabled: boolean; time: string}
}

export const pushKeys = {
  config: ["push", "config"] as const,
  preferences: ["push", "preferences"] as const,
};

export async function getPushConfig() {
  return (await apiClient.get<PushConfig>(`${BASE}/config`)).data;
}

export async function putPushSubscription(body: PushSubscriptionBody) {
  await apiClient.put(`${BASE}/subscriptions`, body);
}

export async function deletePushDevice(id: string) {
  await apiClient.delete(`${BASE}/subscriptions/${encodeURIComponent(id)}`);
}

export async function getPushPreferences() {
  return (await apiClient.get<PushPreferences>(`${BASE}/preferences`)).data;
}

export async function putPushPreferences(preferences: PushPreferences) {
  return (await apiClient.put<PushPreferences>(`${BASE}/preferences`, preferences)).data;
}

export async function sendPushTest() {
  await apiClient.post(`${BASE}/test`, {});
}

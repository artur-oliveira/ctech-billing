import {createHash} from 'node:crypto';
import {readFileSync} from 'node:fs';
import {resolve} from 'node:path';
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest';

const api = vi.hoisted(() => ({
  getPushConfig: vi.fn(),
  putPushSubscription: vi.fn(),
  deletePushDevice: vi.fn(),
}));
vi.mock('@/lib/api/push', async importOriginal => ({...await importOriginal<typeof import('@/lib/api/push')>(), ...api}));

import {PUSH_DISABLED, pushCallbacks} from '@/lib/push/callbacks';
import {currentPushDeviceId, pushDeviceId} from '@/lib/push/device';
import {PUSH_LOGOUT_TIMEOUT_MS, removeThisDevicePush} from '@/lib/push/logout';
import {PUSH_STAMP_KEY, PUSH_USER_KEY} from '@/lib/push/storage';

const ROOT = resolve(__dirname, '../../..');
const ENDPOINT = 'https://fcm.googleapis.com/fcm/send/abc:def-123';

describe('service worker', () => {
  // The worker is copied to public/ by `predev`/`prebuild`; a stale copy would ship an old version of the push logic.
  it('public/sw.js is byte for byte the one of @aoctech/push-client', () => {
    const published = readFileSync(resolve(ROOT, 'public/sw.js'));
    const packaged = readFileSync(resolve(ROOT, 'node_modules/@aoctech/push-client/dist/sw.js'));
    expect(published.equals(packaged)).toBe(true);
  });

  it('has no fetch handler and no cache, so a deploy never serves an old bundle', () => {
    const sw = readFileSync(resolve(ROOT, 'public/sw.js'), 'utf8');
    expect(sw).not.toMatch(/addEventListener\(\s*['"]fetch['"]/);
    expect(sw).not.toMatch(/\bcaches\./);
  });
});

describe('contract with the package', () => {
  // The app removes the stamp by name to force a resend; a rename in the package would silently turn that into a no-op.
  it('the sync stamp key is the one @aoctech/push-client writes', () => {
    const client = readFileSync(resolve(ROOT, 'node_modules/@aoctech/push-client/dist/client.js'), 'utf8');
    expect(client).toContain(`'${PUSH_STAMP_KEY}'`);
  });
});

describe('pushDeviceId', () => {
  // Same rule as pushsvc.SubscriptionID in the API: 16 hex characters of SHA-256(endpoint).
  it('is the first 8 bytes of the SHA-256 of the endpoint, in hex', async () => {
    const expected = createHash('sha256').update(ENDPOINT).digest('hex').slice(0, 16);
    expect(await pushDeviceId(ENDPOINT)).toBe(expected);
  });
});

function stubServiceWorker(subscription: { endpoint: string; unsubscribe: () => Promise<boolean> } | null) {
  const registration = {pushManager: {getSubscription: async () => subscription}};
  vi.stubGlobal('navigator', {...navigator, serviceWorker: {getRegistration: async () => registration}});
}

describe('currentPushDeviceId', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('returns the id of the browser subscription, or null without one', async () => {
    stubServiceWorker({endpoint: ENDPOINT, unsubscribe: async () => true});
    expect(await currentPushDeviceId()).toBe(await pushDeviceId(ENDPOINT));
    stubServiceWorker(null);
    expect(await currentPushDeviceId()).toBeNull();
  });
});

describe('removeThisDevicePush (logout)', () => {
  beforeEach(() => {
    localStorage.clear();
    Object.values(api).forEach(fn => fn.mockReset());
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('cancels the browser subscription BEFORE deleting the server row', async () => {
    const calls: string[] = [];
    stubServiceWorker({endpoint: ENDPOINT, unsubscribe: async () => (calls.push('local'), true)});
    api.deletePushDevice.mockImplementation(async () => {
      calls.push('server');
    });
    await removeThisDevicePush();
    expect(calls).toEqual(['local', 'server']);
    expect(api.deletePushDevice).toHaveBeenCalledWith(await pushDeviceId(ENDPOINT));
  });

  it('a failed DELETE neither leaves the local subscription alive nor blocks logout', async () => {
    const unsubscribe = vi.fn().mockResolvedValue(true);
    stubServiceWorker({endpoint: ENDPOINT, unsubscribe});
    api.deletePushDevice.mockRejectedValue(new Error('network'));
    await expect(removeThisDevicePush()).resolves.toBeUndefined();
    expect(unsubscribe).toHaveBeenCalledTimes(1);
  });

  it('forgets the sync stamp and whose it was, even without a subscription', async () => {
    localStorage.setItem(PUSH_STAMP_KEY, '{"at":1,"kid":"k"}');
    localStorage.setItem(PUSH_USER_KEY, 'u1');
    stubServiceWorker(null);
    await removeThisDevicePush();
    expect(localStorage.getItem(PUSH_STAMP_KEY)).toBeNull();
    expect(localStorage.getItem(PUSH_USER_KEY)).toBeNull();
  });

  it('drops the stamp before any network wait, so the next login resends', async () => {
    localStorage.setItem(PUSH_STAMP_KEY, '{"at":1,"kid":"k"}');
    stubServiceWorker({endpoint: ENDPOINT, unsubscribe: () => new Promise(() => undefined)});
    void removeThisDevicePush();
    expect(localStorage.getItem(PUSH_STAMP_KEY)).toBeNull();
  });

  it('gives up after the timeout when the browser never answers', async () => {
    // Fake timers only here: with them on for every test, a loaded machine could let the 2 s bound fire before the work ran.
    vi.useFakeTimers();
    stubServiceWorker({endpoint: ENDPOINT, unsubscribe: () => new Promise(() => undefined)});
    const done = removeThisDevicePush();
    await vi.advanceTimersByTimeAsync(PUSH_LOGOUT_TIMEOUT_MS + 1);
    await expect(done).resolves.toBeUndefined();
  });
});

describe('pushCallbacks', () => {
  beforeEach(() => Object.values(api).forEach(fn => fn.mockReset()));

  it('getConfig turns a disabled installation into PUSH_DISABLED', async () => {
    api.getPushConfig.mockResolvedValue({enabled: false, max_subscriptions: 10});
    await expect(pushCallbacks.getConfig()).rejects.toThrow(PUSH_DISABLED);
  });

  it('getConfig returns the key and its id', async () => {
    api.getPushConfig.mockResolvedValue({enabled: true, public_key: 'PUB', vapid_kid: 'kid1', max_subscriptions: 10});
    await expect(pushCallbacks.getConfig()).resolves.toEqual({publicKey: 'PUB', kid: 'kid1'});
  });

  it('save sends only the endpoint and the keys, never the client bookkeeping', async () => {
    await pushCallbacks.save({endpoint: ENDPOINT, keys: {p256dh: 'p', auth: 'a'}, vapid_kid: 'k', expiration_time: 123});
    expect(api.putPushSubscription).toHaveBeenCalledWith({endpoint: ENDPOINT, keys: {p256dh: 'p', auth: 'a'}});
  });

  it('remove deletes by the id derived from the endpoint', async () => {
    await pushCallbacks.remove(ENDPOINT);
    expect(api.deletePushDevice).toHaveBeenCalledWith(await pushDeviceId(ENDPOINT));
  });
});

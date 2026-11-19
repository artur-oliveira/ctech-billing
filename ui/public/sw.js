/* @aoctech/push-client service worker. Push only: no fetch handler and no Cache Storage, so a deploy never meets a stale
 * bundle. Classic script (no modules, no build). SW_VERSION is stamped at build time. */
const SW_VERSION = '1.0.1';

const NAVIGATE_MESSAGE_TYPE = 'ctech-push:navigate';
const DEFAULT_TITLE = 'Nova atualização no CTech';
const DEFAULT_PATH = '/';
const MAX_TITLE = 120;
const MAX_BODY = 240;
const MAX_TAG = 64;
const MAX_PATH = 512;
const SUPPORTED_VERSION = 1;

/** Only a path of this origin: starts with a single "/", no backslash, no control character, resolves to the same origin. */
function safePath(value) {
  if (typeof value !== 'string' || value.length === 0 || value.length > MAX_PATH) return DEFAULT_PATH;
  if (value[0] !== '/' || value[1] === '/' || value[1] === '\\') return DEFAULT_PATH;
  // eslint-disable-next-line no-control-regex
  if (/[\\\u0000-\u001f\u007f]/.test(value)) return DEFAULT_PATH;
  try {
    return new URL(value, self.location.origin).origin === self.location.origin ? value : DEFAULT_PATH;
  } catch {
    return DEFAULT_PATH;
  }
}

function text(value, max) {
  return typeof value === 'string' ? value.slice(0, max) : '';
}

/** Reads the Declarative Web Push shape; anything unreadable becomes the generic notification. */
function readPayload(event) {
  let n = null;
  try {
    const json = event.data ? event.data.json() : null;
    if (json && typeof json === 'object' && json.notification && typeof json.notification === 'object') n = json.notification;
  } catch {
    n = null;
  }
  const data = n && n.data && typeof n.data === 'object' ? n.data : {};
  const title = text(n && n.title, MAX_TITLE).trim();
  return {
    title: title || DEFAULT_TITLE,
    body: text(n && n.body, MAX_BODY),
    tag: text(n && n.tag, MAX_TAG) || undefined,
    path: safePath(n && n.navigate),
    silent: !!(n && n.silent === true),
    version: data.v === SUPPORTED_VERSION ? data.v : undefined,
    id: text(data.id, MAX_TAG) || undefined,
    type: text(data.type, MAX_TAG) || undefined,
  };
}

self.addEventListener('install', () => {
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener('push', (event) => {
  const p = readPayload(event);
  event.waitUntil(
    self.clients.matchAll({type: 'window', includeUncontrolled: true}).then((windows) => {
      // A focused window already got the toast over the WebSocket. The notification is still shown, silently: Chrome
      // requires a visible one (userVisibleOnly) and Safari revokes the subscription after silent pushes.
      const focused = windows.some((c) => c.focused);
      return self.registration.showNotification(p.title, {
        body: p.body,
        tag: p.tag,
        lang: 'pt-BR',
        silent: p.silent || focused,
        data: {v: p.version, id: p.id, type: p.type, navigate: p.path},
      });
    }),
  );
});

self.addEventListener('notificationclick', (event) => {
  event.notification.close();
  const path = safePath(event.notification.data && event.notification.data.navigate);
  event.waitUntil(
    self.clients.matchAll({type: 'window', includeUncontrolled: true}).then((windows) => {
      const open = windows.find((c) => {
        try {
          return new URL(c.url).origin === self.location.origin;
        } catch {
          return false;
        }
      });
      if (open) {
        return open.focus().then(() => open.postMessage({type: NAVIGATE_MESSAGE_TYPE, url: path}));
      }
      return self.clients.openWindow(new URL(path, self.location.origin).href);
    }),
  );
});

// pushsubscriptionchange has no handler on purpose: the worker holds no token, so the page repairs the subscription on the
// next load (syncOnLoad) and the server drops dead ones on 404/410.
self.SW_VERSION = SW_VERSION;

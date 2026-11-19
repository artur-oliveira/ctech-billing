/** The sync stamp @aoctech/push-client keeps per browser (`dist/client.js`; push.test.ts pins the name). */
export const PUSH_STAMP_KEY = 'ctech-push:last-sync';
/** Whose subscription this browser last synced; a different person signing in drops the stamp so it is resent. */
export const PUSH_USER_KEY = 'ctech-billing:push-user';

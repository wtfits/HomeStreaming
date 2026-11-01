import { expect, Page } from '@playwright/test';

export const shotDir = process.env.HS_SHOTS || 'test-results/shots';

/** Waits until the player's video has advanced at least `secs` seconds of playback. */
export async function expectPlaying(page: Page, secs = 2) {
  await expect
    .poll(async () => page.evaluate(() => {
      const v = document.querySelector('video');
      return v ? v.currentTime : 0;
    }), { timeout: 60_000 })
    .toBeGreaterThanOrEqual(secs);
}

export async function videoTime(page: Page): Promise<number> {
  return page.evaluate(() => {
    const v = document.querySelector('video');
    return v ? v.currentTime : 0;
  });
}

/** Opens an item page by searching for its title. */
export async function openItem(page: Page, query: string, type = '') {
  const res = await page.request.get('/api/v1/items?search=' + encodeURIComponent(query) + (type ? '&type=' + type : ''), {
    headers: { Authorization: 'Bearer ' + (await token(page)) },
  });
  const body = await res.json();
  expect(body.items.length, 'search ' + query).toBeGreaterThan(0);
  await page.goto('/#/item/' + body.items[0].id);
  return body.items[0];
}

export async function token(page: Page): Promise<string> {
  if (!page.url().startsWith('http')) await page.goto('/');
  return page.evaluate(() => localStorage.getItem('hs.token') || '');
}

export async function api(page: Page, path: string) {
  const res = await page.request.get('/api/v1' + path, { headers: { Authorization: 'Bearer ' + (await token(page)) } });
  return res.json();
}

/** Dispatches a key with a specific legacy keyCode (e.g. webOS Back = 461). */
export async function pressKeyCode(page: Page, keyCode: number) {
  await page.evaluate((kc) => {
    const ev = new KeyboardEvent('keydown', { bubbles: true, cancelable: true });
    Object.defineProperty(ev, 'keyCode', { get: () => kc });
    Object.defineProperty(ev, 'which', { get: () => kc });
    (document.activeElement || document.body).dispatchEvent(ev);
  }, keyCode);
}

/**
 * Pretends the server gets updated: each call of the returned function makes
 * /system/info report a newer version ("<real>-next1", "-next2", ...).
 */
export async function fakeServerUpdate(page: Page): Promise<() => void> {
  let n = 0;
  await page.route('**/api/v1/system/info', async (route) => {
    const res = await route.fetch();
    const body = await res.json();
    if (n > 0) body.version = String(body.version) + '-next' + n;
    await route.fulfill({ response: res, json: body });
  });
  return () => {
    n++;
  };
}

/** Marks the current document; markerGone() turns true after a reload. */
export async function markDocument(page: Page) {
  await page.evaluate(() => ((window as any).__hsMarker = 1));
}
export async function markerGone(page: Page): Promise<boolean> {
  try {
    return await page.evaluate(() => (window as any).__hsMarker !== 1);
  } catch (_) {
    return false; // navigation in progress; poll again
  }
}

/** What the page does when the TV app or iPhone app comes back to the front. */
export async function bringToFront(page: Page) {
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
}

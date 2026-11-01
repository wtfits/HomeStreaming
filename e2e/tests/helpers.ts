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

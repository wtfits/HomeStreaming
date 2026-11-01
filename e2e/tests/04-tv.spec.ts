import { expect, test } from '@playwright/test';
import { bringToFront, expectPlaying, fakeServerUpdate, markDocument, markerGone, openItem, pressKeyCode, shotDir, videoTime } from './helpers';

const isCard = () => (document.activeElement ? (document.activeElement as HTMLElement).className : '');

test('webOS TV mode: D-pad home → item → play → seek → back', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('html')).toHaveClass(/tv/);
  await expect(page.getByRole('heading', { name: 'Recently Added' })).toBeVisible();
  await page.waitForTimeout(300);
  // Move down from the nav into the first row of cards.
  for (let i = 0; i < 4; i++) {
    const cls = await page.evaluate(isCard);
    if (cls.indexOf('card') >= 0) break;
    await page.keyboard.press('ArrowDown');
    await page.waitForTimeout(150);
  }
  expect(await page.evaluate(isCard)).toContain('card');
  await page.keyboard.press('ArrowRight');
  await page.waitForTimeout(150);
  await page.screenshot({ path: shotDir + '/13-tv-home.png' });
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/#\/item\//);
  await page.waitForTimeout(400);
  // The primary action is focused automatically.
  const focused = await page.evaluate(() => (document.activeElement as HTMLElement).textContent || '');
  expect(focused).toMatch(/Play|Resume/);
  await page.screenshot({ path: shotDir + '/14-tv-item.png' });
  await page.keyboard.press('Enter');
  await expect(page).toHaveURL(/#\/play\//);
  await expectPlaying(page, 1);
  const before = await videoTime(page);
  await page.keyboard.press('ArrowRight'); // +10 s
  await expect.poll(() => videoTime(page)).toBeGreaterThanOrEqual(Math.min(before + 8, 15));
  await page.keyboard.press('ArrowUp'); // show controls, focus play
  await page.waitForTimeout(200);
  await page.screenshot({ path: shotDir + '/15-tv-player.png' });
  // webOS Back: hides the focused controls while playing, otherwise leaves the player.
  await pressKeyCode(page, 461);
  await page.waitForTimeout(300);
  if (/#\/play\//.test(page.url())) await pressKeyCode(page, 461);
  await expect(page).toHaveURL(/#\/item\//);
});

test('webOS: picks up a server update after playback, never during it', async ({ page }) => {
  const update = await fakeServerUpdate(page);
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Recently Added' })).toBeVisible();
  await openItem(page, 'Movie Name', 'movie');
  await page.getByRole('link', { name: /^▶ (Play|Resume)/ }).click();
  await expectPlaying(page, 1);
  await markDocument(page);

  update();
  await bringToFront(page);
  await page.waitForTimeout(1500);
  expect(await markerGone(page)).toBe(false); // still the same page
  await expect(page.getByTestId('update-bar')).toHaveCount(0); // nothing over the video
  const t0 = await videoTime(page);
  await expect.poll(() => videoTime(page)).toBeGreaterThan(t0); // still playing

  // Leaving the player (Back) applies the update.
  await pressKeyCode(page, 461);
  await page.waitForTimeout(300);
  if ((await page.url()).indexOf('#/play/') >= 0) await pressKeyCode(page, 461);
  await expect.poll(() => markerGone(page)).toBe(true);
  await expect(page.locator('.toast')).toContainText(/Updated to .*-next1/);
  await page.screenshot({ path: shotDir + '/16-tv-updated.png' });
});

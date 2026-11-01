import { expect, test } from '@playwright/test';
import { expectPlaying, pressKeyCode, shotDir, videoTime } from './helpers';

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

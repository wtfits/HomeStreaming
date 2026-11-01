import { expect, test } from '@playwright/test';
import { expectPlaying, openItem, shotDir } from './helpers';

test('iPhone layout, PWA manifest and inline remux playback', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Recently Added' })).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow).toBeLessThanOrEqual(0);
  await expect(page.locator('link[rel="manifest"]')).toHaveAttribute('href', '/manifest.webmanifest');
  await expect(page.locator('meta[name="apple-mobile-web-app-capable"]')).toHaveAttribute('content', 'yes');
  const manifest = await page.request.get('/manifest.webmanifest');
  expect(manifest.status()).toBe(200);
  expect((await manifest.json()).display).toBe('standalone');
  await page.screenshot({ path: shotDir + '/10-iphone-home.png', fullPage: true });

  await openItem(page, "Widow's Bay", 'series');
  await page.screenshot({ path: shotDir + '/11-iphone-series.png', fullPage: true });
  const ep = page.locator('.episode').first();
  await ep.tap();
  const pi = page.waitForResponse((r) => r.url().includes('/play-info'));
  await page.getByRole('link', { name: /^▶ Play$/ }).tap();
  const body = await (await pi).json();
  expect(body.method).toBe('remux'); // MKV is not playable on iOS
  expect(body.reasons).toContain('container');
  await expect(page.locator('video')).toHaveAttribute('playsinline', '');
  await expectPlaying(page);
  await page.getByTestId('video').tap();
  await page.getByTestId('play-pause').tap();
  await expect.poll(() => page.evaluate(() => (document.querySelector('video') as HTMLVideoElement).paused)).toBe(true);
  await page.screenshot({ path: shotDir + '/12-iphone-player.png' });
});

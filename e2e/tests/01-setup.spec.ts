import { expect, test } from '@playwright/test';
import { shotDir } from './helpers';

test('first-run setup, add library, scan', async ({ page }) => {
  const media = process.env.HS_MEDIA_DIR!;
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Welcome to HomeStreaming' })).toBeVisible();
  await expect(page.getByText('ffmpeg found')).toBeVisible();
  await page.screenshot({ path: shotDir + '/01-setup-ffmpeg.png' });
  await page.getByRole('button', { name: 'Next' }).click();

  await page.getByLabel('Username').fill('can');
  await page.getByLabel('Password', { exact: true }).fill('password123');
  await page.getByLabel('Confirm password').fill('password123');
  await page.getByRole('button', { name: 'Create account' }).click();

  await expect(page.getByRole('heading', { name: 'Step 3 · Your media folder' })).toBeVisible();
  await page.getByPlaceholder('F:\\torrent').fill(media);
  await page.screenshot({ path: shotDir + '/02-setup-library.png' });
  await page.getByRole('button', { name: 'Scan' }).click();
  await expect(page.getByText('Your library is ready.')).toBeVisible({ timeout: 90_000 });
  await page.getByRole('button', { name: 'Go to Home' }).click();
  await expect(page.getByRole('heading', { name: 'Recently Added' })).toBeVisible();

  // Wait until probing/thumbnails are done so later projects see final state.
  await expect
    .poll(async () => {
      const tok = await page.evaluate(() => localStorage.getItem('hs.token'));
      const r = await page.request.get('/api/v1/libraries', { headers: { Authorization: 'Bearer ' + tok } });
      const lib = (await r.json()).items[0];
      return lib.counts.movies + '/' + lib.counts.series + '/' + lib.counts.episodes + '/' + lib.counts.pending + '/' + lib.scanning;
    }, { timeout: 90_000 })
    .toBe('4/5/8/0/false');
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Movies' })).toBeVisible();
  await page.screenshot({ path: shotDir + '/03-home-desktop.png', fullPage: true });
  await page.context().storageState({ path: 'state.json' });
});

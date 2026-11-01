import { expect, test } from '@playwright/test';
import { api, expectPlaying, openItem, shotDir, videoTime } from './helpers';

test.describe.configure({ mode: 'serial' });

test('library grid with tabs', async ({ page }) => {
  await page.goto('/');
  await page.getByRole('link', { name: 'Torrent' }).first().click();
  await expect(page.locator('.grid .card')).toHaveCount(4);
  await page.getByRole('button', { name: /Shows/ }).click();
  await expect(page.locator('.grid .card')).toHaveCount(5);
  await expect(page.locator('.grid .card', { hasText: 'Arrested Development' })).toHaveCount(1);
  await page.screenshot({ path: shotDir + '/04-library-shows.png' });
});

test('direct play H.264 MP4', async ({ page }) => {
  await openItem(page, 'Movie Name', 'movie');
  await page.screenshot({ path: shotDir + '/05-item-movie.png' });
  const pi = page.waitForResponse((r) => r.url().includes('/play-info'));
  await page.getByRole('link', { name: /^▶ Play$/ }).click();
  const body = await (await pi).json();
  expect(body.method).toBe('direct');
  await expectPlaying(page);
  await expect(page.getByTestId('method')).toContainText('Direct play');

  // Volume slider + mute button drive the <video> element and persist across reloads.
  const vol = () => page.getByTestId('video').evaluate((v: HTMLVideoElement) => ({ volume: v.volume, muted: v.muted }));
  await page.getByTestId('volume').fill('30');
  await expect.poll(async () => (await vol()).volume).toBeCloseTo(0.3, 2);
  await page.getByTestId('mute').click();
  await expect.poll(async () => (await vol()).muted).toBe(true);
  await expect(page.getByTestId('volume')).toHaveValue('0');
  await page.screenshot({ path: shotDir + '/05b-player-volume.png' });
  await page.reload();
  await expect.poll(vol).toEqual({ volume: 0.3, muted: true });
  await page.getByTestId('mute').click();
  await expect.poll(async () => (await vol()).muted).toBe(false);
  await expect(page.getByTestId('volume')).toHaveValue('30');
});

test('HEVC 10-bit HDR transcodes with tone mapping', async ({ page }) => {
  await openItem(page, 'Another Film', 'movie');
  const pi = page.waitForResponse((r) => r.url().includes('/play-info'));
  await page.getByRole('link', { name: /^▶ Play$/ }).click();
  const body = await (await pi).json();
  expect(body.method).toBe('transcode');
  expect(body.reasons).toContain('video_codec');
  expect(body.transcode.video.tonemap).toBe(true);
  await expectPlaying(page);
  await page.mouse.move(400, 400);
  await page.screenshot({ path: shotDir + '/06-player-transcode.png' });
});

test('AC-3 track remuxes with audio encode, then switch Rus → Eng', async ({ page }) => {
  await openItem(page, 'Third Movie', 'movie');
  await page.getByLabel('Audio').selectOption({ index: 0 }); // Russian AC-3 5.1
  const pi = page.waitForResponse((r) => r.url().includes('/play-info'));
  await page.getByRole('link', { name: /^▶ Play$/ }).click();
  const body = await (await pi).json();
  expect(body.method).toBe('remux');
  expect(body.transcode.audio.action).toBe('encode');
  expect(body.reasons).toContain('audio_codec');
  await expectPlaying(page);

  await page.mouse.move(300, 300);
  await page.getByTestId('audio-menu').click();
  const pi2 = page.waitForResponse((r) => r.url().includes('/play-info'));
  await page.getByRole('button', { name: /English/ }).click();
  const body2 = await (await pi2).json();
  expect(body2.audioTrackId).toBe(2);
  expect(body2.transcode.audio.action).toBe('copy');
  await expectPlaying(page, 3);
});

test('seek to 80% in a transcode, resume from Continue Watching', async ({ page }) => {
  await page.goto('/');
  await page.evaluate(() => localStorage.setItem('hs.forceTranscode', '1'));
  const item = await openItem(page, 'Movie Name', 'movie');
  const pi = page.waitForResponse((r) => r.url().includes('/play-info'));
  await page.getByRole('link', { name: /Play/ }).first().click();
  expect((await (await pi).json()).method).toBe('transcode');
  await expectPlaying(page);
  // Seek to 80 % (72 s of 90 s): the server restarts ffmpeg at that segment.
  await page.evaluate(() => {
    const r = document.querySelector('.seek input') as HTMLInputElement;
    r.value = '72000';
    r.dispatchEvent(new Event('change', { bubbles: true }));
  });
  await expect.poll(() => videoTime(page), { timeout: 60_000 }).toBeGreaterThanOrEqual(73);
  await page.evaluate(() => (document.querySelector('video') as HTMLVideoElement).pause());
  await page.waitForTimeout(500);
  await page.evaluate(() => localStorage.removeItem('hs.forceTranscode'));
  await page.goto('/');
  await page.reload();
  const row = page.locator('.row', { has: page.getByRole('heading', { name: 'Continue Watching' }) });
  await expect(row.locator('.card', { hasText: 'Movie Name' })).toBeVisible();
  await page.screenshot({ path: shotDir + '/07-home-continue.png' });
  const ud = (await api(page, '/items/' + item.id)).userData;
  expect(ud.positionMs).toBeGreaterThan(70000);
  await page.goto('/#/item/' + item.id);
  await page.getByRole('link', { name: /Resume from 1:1/ }).click();
  await expect.poll(() => videoTime(page), { timeout: 60_000 }).toBeGreaterThanOrEqual(73);
});

test('external subtitle renders a cue', async ({ page }) => {
  const ep = await openItem(page, 'Arrested Development', 'episode');
  // The S01E07 episode has a sidecar .srt.
  const eps = await api(page, '/items?type=episode&seriesId=' + ep.seriesId + '&sort=episode');
  const e7 = eps.items.find((x: any) => x.seasonNumber === 1 && x.episodeNumber === 7);
  await page.goto('/#/item/' + e7.id);
  const subSelect = page.getByLabel('Subtitles');
  const options = await subSelect.locator('option').allTextContents();
  await subSelect.selectOption({ index: options.length - 1 });
  await page.getByRole('link', { name: /^▶ Play$/ }).click();
  await expect
    .poll(
      () =>
        page.evaluate(() => {
          const v = document.querySelector('video') as HTMLVideoElement | null;
          const tt = v && v.textTracks[0];
          return tt && tt.activeCues ? tt.activeCues.length : 0;
        }),
      { timeout: 30_000 }
    )
    .toBeGreaterThan(0);
  await page.mouse.move(200, 200);
  await page.screenshot({ path: shotDir + '/08-player-subtitles.png' });
});

test('mark played moves Next Up to the next episode', async ({ page }) => {
  const ep = await openItem(page, 'Arrested Development', 'episode');
  const eps = await api(page, '/items?type=episode&seriesId=' + ep.seriesId + '&sort=episode');
  const e7 = eps.items.find((x: any) => x.seasonNumber === 1 && x.episodeNumber === 7);
  await page.goto('/#/item/' + e7.id);
  await page.getByRole('button', { name: 'Mark played' }).click();
  await expect(page.getByRole('button', { name: 'Mark unplayed' })).toBeVisible();
  await page.goto('/');
  const row = page.locator('.row', { has: page.getByRole('heading', { name: 'Next Up' }) });
  await expect(row.locator('.card', { hasText: 'S1·E8' })).toBeVisible();
  await page.goto('/#/item/' + ep.seriesId);
  await expect(page.getByRole('link', { name: /Play S1·E8/ })).toBeVisible();
  await page.screenshot({ path: shotDir + '/09-series.png', fullPage: true });
});

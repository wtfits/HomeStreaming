import { defineConfig, devices } from '@playwright/test';

const base = process.env.HS_BASE_URL || 'http://127.0.0.1:8420';
const launchOptions = { args: ['--autoplay-policy=no-user-gesture-required'] };
const WEBOS_UA =
  'Mozilla/5.0 (Web0S; Linux/SmartTV) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/79.0.3945.79 Safari/537.36 WebAppManager';

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  timeout: 120_000,
  expect: { timeout: 20_000 },
  reporter: [['list']],
  outputDir: './test-results',
  use: { baseURL: base, trace: 'retain-on-failure', screenshot: 'only-on-failure', launchOptions },
  projects: [
    { name: 'setup', testMatch: /01-setup\.spec\.ts/, use: { ...devices['Desktop Chrome'], launchOptions } },
    { name: 'desktop', testMatch: /02-desktop\.spec\.ts/, dependencies: ['setup'], use: { ...devices['Desktop Chrome'], storageState: 'state.json', launchOptions } },
    {
      name: 'iphone',
      testMatch: /03-iphone\.spec\.ts/,
      dependencies: ['setup'],
      use: { ...devices['iPhone 13'], browserName: 'chromium', storageState: 'state.json', launchOptions },
    },
    {
      name: 'webos',
      testMatch: /04-tv\.spec\.ts/,
      dependencies: ['setup'],
      use: { browserName: 'chromium', userAgent: WEBOS_UA, viewport: { width: 1920, height: 1080 }, storageState: 'state.json', launchOptions },
    },
  ],
});

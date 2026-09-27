import { defineConfig } from '@playwright/test';

const executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH;

export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  expect: { timeout: 15_000 },
  use: {
    baseURL: process.env.E2E_BASE_URL ?? 'http://127.0.0.1:8080',
    browserName: 'chromium',
    launchOptions: executablePath
      ? { executablePath, args: ['--no-sandbox', '--disable-dev-shm-usage'] }
      : undefined,
  },
  reporter: [['list']],
});

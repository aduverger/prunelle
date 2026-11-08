import { defineConfig } from '@playwright/test';
export default defineConfig({
  testDir: './tests',
  testMatch: '**/*.spec.ts',
  fullyParallel: false,
  workers: 1,
  timeout: 45_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL: 'http://127.0.0.1:15173',
    viewport: { width: 1550, height: 1000 },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  webServer: {
    command: 'PRUNELLE_SERVER=http://127.0.0.1:17391 pnpm dev --port 15173',
    url: 'http://127.0.0.1:15173',
    reuseExistingServer: false,
  },
});

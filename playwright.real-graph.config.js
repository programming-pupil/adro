const { defineConfig, devices } = require('@playwright/test');

module.exports = defineConfig({
  testDir: './e2e',
  testMatch: 'graph-browser.spec.js',
  timeout: 30 * 60 * 1000,
  expect: { timeout: 30_000 },
  fullyParallel: false,
  workers: 1,
  reporter: 'list',
  use: {
    ...devices['Desktop Chrome'],
    baseURL: process.env.ADRO_GRAPH_BROWSER_WEB_URL || 'http://127.0.0.1:18087',
    trace: 'on',
    screenshot: 'on',
    video: 'on'
  }
});

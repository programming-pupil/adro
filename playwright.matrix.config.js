const { defineConfig, devices } = require('@playwright/test');

module.exports = defineConfig({
  testDir: './e2e',
  testMatch: 'platform-matrix.spec.js',
  timeout: 45_000,
  expect: { timeout: 30_000 },
  fullyParallel: false,
  workers: 1,
  reporter: process.env.CI ? 'line' : 'list',
  use: {
    baseURL: 'http://127.0.0.1:18081',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure'
  },
  projects: [
    { name: 'chromium-desktop', use: { ...devices['Desktop Chrome'] } },
    { name: 'firefox-desktop', use: { ...devices['Desktop Firefox'] } },
    { name: 'webkit-desktop', use: { ...devices['Desktop Safari'] } },
    { name: 'chromium-mobile', use: { ...devices['Pixel 7'] } },
    { name: 'webkit-mobile', use: { ...devices['iPhone 15'] } }
  ],
  webServer: [
    {
      command: 'ADRO_AUTH_MODE=required ADRO_ADMIN_USERNAME=admin ADRO_ADMIN_PASSWORD=AdminPass123! go run ./cmd/adro-api -addr :18080 -artifact-root ./var/e2e-artifacts',
      url: 'http://127.0.0.1:18080/readyz',
      timeout: 120_000,
      reuseExistingServer: !process.env.CI
    },
    {
      command: 'node e2e/static-server.js 18081',
      url: 'http://127.0.0.1:18081',
      timeout: 30_000,
      reuseExistingServer: !process.env.CI
    }
  ]
});

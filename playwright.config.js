const { defineConfig, devices } = require('@playwright/test');

module.exports = defineConfig({
  testDir: './e2e',
  testIgnore: 'platform-matrix.spec.js',
  timeout: 30_000,
  expect: { timeout: 30_000 },
  fullyParallel: false,
  reporter: process.env.CI ? [['line'], ['html', { open: 'never' }]] : 'list',
  use: {
    ...devices['Desktop Chrome'],
    baseURL: 'http://127.0.0.1:18081',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure'
  },
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

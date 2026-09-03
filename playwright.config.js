const { defineConfig, devices } = require('@playwright/test');
const path = require('path');

// The API uses durable files by default. Give every Playwright process its own
// state namespace so fixed fixture names remain repeatable across local runs.
const e2eStateDir = `./var/e2e-state-${process.pid}`;
const e2eExecutor = process.env.ADRO_E2E_EXECUTOR || path.resolve('scripts/e2e-executor.sh');
const e2eGo = process.env.ADRO_GO_BIN || path.resolve('scripts/e2e-go.sh');
const executorEnv = `ADRO_EXECUTOR=${JSON.stringify(e2eExecutor)}`;

module.exports = defineConfig({
  testDir: './e2e',
  testIgnore: 'platform-matrix.spec.js',
  timeout: 30_000,
  expect: { timeout: 30_000 },
  fullyParallel: false,
  // The acceptance specs intentionally share one in-memory control plane and
  // the fixed local workspace. Keep one worker so mutations cannot race while
  // a browser is refreshing the same workspace snapshot.
  workers: 1,
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
      command: `${executorEnv} ADRO_HOME=${e2eStateDir} ADRO_ALLOWED_ORIGINS=http://127.0.0.1:18081,http://localhost:18081,http://[::1]:18081 ADRO_AUTH_MODE=required ADRO_ADMIN_USERNAME=admin ADRO_ADMIN_PASSWORD=AdminPass123! ${JSON.stringify(e2eGo)} run ./cmd/adro-api -addr :18080 -artifact-root ./var/e2e-artifacts`,
      url: 'http://127.0.0.1:18080/readyz',
      timeout: 120_000,
      // Reusing a local API would also reuse its auth/session snapshot. That
      // makes fixed acceptance credentials depend on a previous run and can
      // leave the administrator in the temporary login lockout window.
      reuseExistingServer: false
    },
    {
      command: 'node e2e/static-server.js 18081',
      url: 'http://127.0.0.1:18081',
      timeout: 30_000,
      reuseExistingServer: false
    }
  ]
});

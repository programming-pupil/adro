## Browser acceptance tests

The suite is a repeatable acceptance check for the ADRO local profile. The
Playwright web server starts the Go API, which discovers the real local coding
executable (`claude`, `codex`, or `claude-code`), and a static server for
`apps/web`. No external orchestration service or fake runtime is started.

```bash
npm install
npx playwright install chromium firefox webkit
npm run test:e2e
npm run test:e2e:matrix
```

The stateful suite exercises every navigation view, requirement/Bug creation,
attachments, Agent bindings, resource actions, authentication and menu RBAC.
The run-specific tests use the local process boundary and verify that snapshots
contain a session, work directory, process result and Git provenance when a
repository is configured. The browser suite does not claim that an AI model
completed a delivery: model credentials, repository access and test-environment
credentials are deployment prerequisites and are reported as unavailable.

For model-backed, API-level acceptance use the separate native-process suite:

```bash
make real-e2e
```

It uses the installed `claude`, `codex`, or `claude-code` executable, creates a
temporary Git fixture, and verifies the full Requirement -> development -> real
unit/integration checks -> linked Bug -> original-session repair -> revalidation
-> report path. It does not start Docker or use the deterministic MockProvider.

The separate read-only matrix covers desktop Chromium, Firefox and WebKit plus
Chromium and WebKit mobile device profiles without duplicating mutations.
Emulated WebKit/mobile results are regression evidence, not a claim about
physical Safari, iOS, or Android devices. For a complete real workflow, start
ADRO with `./start.sh --no-open`, submit a Requirement in the workbench, and
retain the resulting run snapshot, evidence bundle and report as release
evidence.

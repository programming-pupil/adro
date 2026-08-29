## Browser acceptance tests

The suite is a repeatable acceptance check for the dependency-free reference
profile. It starts the Go API with `MockProvider` and a static server for
`apps/web`, then exercises every navigation view and the user-visible flows.

```bash
npm install
npx playwright install chromium firefox webkit
npm run test:e2e
npm run test:e2e:matrix
```

The tests are intentionally honest about the profile: the screenshot test
verifies immutable ArtifactStore persistence and the provider attachment
boundary using MockProvider. To test a real Multica deployment, start the API
with `ADRO_PROVIDER=multica`, set `ADRO_MULTICA_URL` and
`ADRO_MULTICA_TOKEN`, and set `ADRO_MULTICA_AGENT_ID` to a real workspace Agent
UUID if Work Items should be assigned automatically. The adapter verifies
`/api/config` plus readiness, creates native Issues with the required
workspace/title fields, and uses `/api/issues/{id}/rerun` when a generic
`/api/runs` route is absent. Run snapshot/message/cancel/usage and cursor event
coverage still requires the corresponding upstream routes (or
`ADRO_MULTICA_WS_URL`); report those as unavailable instead of treating a
successful health check as full provider conformance.

The stateful workflow suite runs once in desktop Chromium. The separate
read-only matrix covers desktop Chromium, Firefox and WebKit plus Chromium and
WebKit mobile device profiles without duplicating mutations. Emulated
WebKit/mobile results are regression evidence, not a claim about physical
Safari, iOS, or Android devices. Run `scripts/multica-conformance.mjs` for the
real upstream boundary and retain its JSON report as release evidence.

# Compatibility matrix

| Surface | Automated evidence | Claim |
| --- | --- | --- |
| Go API | Go 1.24 on CI Ubuntu x86_64; local verification on macOS x86_64 | source/build compatibility only |
| Startup | Bash on macOS/Linux; portable permission regression | single-node bootstrap only |
| Desktop Chromium | full seven-test stateful Playwright workflow | supported reference browser |
| Desktop Firefox | read-only login/navigation/i18n/layout matrix | smoke-tested |
| Desktop WebKit | read-only login/navigation/i18n/layout matrix | Safari-engine emulation only |
| Chromium mobile profile | Pixel 7 emulation smoke | responsive regression only |
| WebKit mobile profile | iPhone 15 emulation smoke | responsive regression only |
| Physical Safari/iOS/Android | no device-lab result | blocked for GA claims |
| Docker/Compose | static configuration checks; Docker unavailable in the CTW-22 runtime | not runtime-verified here |
| Kubernetes/Helm | single replica, Recreate strategy, schema-restricted local backends | reference chart only |
| Multica public API | native Agent/Issue/rerun adapter contract | reference-only until real conformance passes |
| Multica run/session/workdir/attachment/daemon WebSocket/repair | `scripts/multica-conformance.mjs` | blocked unless its retained report exits 0 |

Browser emulation and contract fixtures must not be presented as physical
device, production infrastructure, or real upstream evidence.

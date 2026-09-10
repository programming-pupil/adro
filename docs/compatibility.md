# Compatibility matrix

| Surface | Evidence | Claim |
| --- | --- | --- |
| Go API | Go tests on macOS/Linux | source and build compatibility |
| Native startup | `start.sh`, Bash checks, local health smoke | supported single-node profile; no Docker prerequisite |
| Local executor | shared startup/API registry, PATH/login-shell/Desktop discovery, real client version probe | installed coding clients with a tested adapter plus compatible explicit argv executors |
| Agent configuration | runtime model catalog, thinking/service options, runtime Skills and MCP bindings | per-Agent revisioned execution policy; unsupported model selection is explicit |
| Workflow migration | portable ZIP plus read-only compatible PostgreSQL converter | user-authored workspace state, references and verified attachments; credentials and live execution state are excluded |
| Desktop browsers | Playwright Chromium/Firefox/WebKit matrix | responsive reference workbench |
| Mobile browsers | Chromium/WebKit emulation | responsive regression only |
| Kubernetes/Compose | static YAML and schema checks | deployment references; local runtime remains native |
| External execution plugins | versioned `ExecutionProvider` SPI | optional community boundary; not bundled or required |

Browser emulation and contract fixtures must not be presented as physical
device, production infrastructure, or real model-quality evidence.

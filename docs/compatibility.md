# Compatibility matrix

| Surface | Evidence | Claim |
| --- | --- | --- |
| Go API | Go tests on macOS/Linux | source and build compatibility |
| Native startup | `start.sh`, Bash checks, local health smoke | supported single-node profile; no Docker prerequisite |
| Local executor | PATH discovery plus real Claude Code version probe | Claude Code, Codex, and compatible argv clients |
| Desktop browsers | Playwright Chromium/Firefox/WebKit matrix | responsive reference workbench |
| Mobile browsers | Chromium/WebKit emulation | responsive regression only |
| Kubernetes/Compose | static YAML and schema checks | deployment references; local runtime remains native |
| External execution plugins | versioned `ExecutionProvider` SPI | optional community boundary; not bundled or required |

Browser emulation and contract fixtures must not be presented as physical
device, production infrastructure, or real model-quality evidence.

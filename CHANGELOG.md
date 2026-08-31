# Changelog

## Unreleased

- Added the provider-independent local control-plane reference profile, HTTP
  API, event cursor, artifact driver, runner supervisor, capability registry,
  multilingual workbench, migrations, OpenAPI and deployment scaffolding.
- Hardened requirement repository relations so a registered repository from a
  different workspace cannot be cloned into an execution workdir; added a
  regression test for the cross-workspace rejection.
- Clarified that Git/CI and DingTalk/Feishu production connectors are external
  SPI plugins and release prerequisites, not built-in adapters of the local
  single-node profile.

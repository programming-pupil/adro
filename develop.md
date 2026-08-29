根据父场景和方案 issue 的最终交接实现“多 Multica Agent 路由与可审计诊断”。

必须在 `/Users/shareit/program/github/adro` 现有代码上工作，保护已有修改。补齐后端配置/Provider/WorkItem 传递、必要的 API 与 WebUI 诊断、中英文文案、README/GA matrix 和自动化测试。保持 `ADRO_MULTICA_AGENT_ID` 向后兼容，不泄露映射内容中的敏感信息。

提交前至少运行 `go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./...` 以及相关前端/E2E 检查。记录首次 run 的 session/workdir 证据。QA 打回时必须继续本 issue，不得创建替代研发 issue；先复现再修复并重新提测。

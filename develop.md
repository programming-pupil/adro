根据需求文档和技术设计实现 ADRO 的多 Agent 全自动交付闭环。

所有执行都经过 `ExecutionProvider` SPI；本地 profile 使用真实
`LocalProvider`，自动发现 claude/codex/claude-code 并以受控 argv 启动。
提交前至少运行 `go test ./...`、`go test -race ./...`、`go vet ./...`、
`go build ./...`、`make contracts` 以及相关前端/E2E 检查。QA 打回时必须
继续原 session 和 worktree，不得创建替代研发任务。

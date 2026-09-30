# adro

adro 正在进行 v2 重建。本分支包含沿用的 Go 库、迁移中的测试资产和首批静态检查；阶段 0 尚未完成。新的单一可执行程序及 server 命令尚未提供。

`v1-final` 是只读参考标签。本分支不维护 v1 兼容；旧部署和启动说明已移除。

## 本地验证

使用 `go.mod` 声明的 Go 版本：

```sh
go build ./...
go test -race ./core/errs ./internal/upstream/scan ./internal/upstream/supervisor
go test -race ./tools/namingdeny/...
```

以上命令只验证现有组件，不代表阶段 0 或存储验收通过。全仓名称检查仍因遗留内容和待澄清的命名规则冲突失败。其余门禁、生产装配与 PostgreSQL 集成仍在开发中。

验证 ADRO 通过真实 Multica workspace 和本机 Codex runtime 完成一次可审计的多 Agent 交付闭环。

目标能力：将当前单一 `ADRO_MULTICA_AGENT_ID` 扩展为按 ADRO 成员/角色选择不同 Multica Agent，并在诊断、README 和测试中可验证；保留单一默认 Agent 的向后兼容。

必须覆盖：方案设计、真实代码实现、单元/并发/构建验证、独立 QA、一次真实打回、原研发 issue 与原研发 Agent 恢复修复、再次提测、发布 GA 判定。严禁伪造缺陷、测试或上游能力。

连续性证据：记录研发首次与打回后 run 的 task ID、session ID、work_dir、issue ID；打回不得创建新的研发 issue。

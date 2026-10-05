<!--
感谢提交 PR！请先阅读 CONTRIBUTING.md，并确认下面的清单项。
不熟悉的字段可以留空，维护者会协助补齐。
-->

## 变更说明

<!-- 这个 PR 做了什么？为什么需要它？关联 Issue 请写 Closes #123 -->

Closes #

## 变更类型

- [ ] 新功能（feature）
- [ ] Bug 修复（bugfix）
- [ ] 重构 / 性能
- [ ] 文档
- [ ] 测试 / CI
- [ ] 其他（请说明）：

## 自测清单

- [ ] 后端：`cd backend && go build ./... && go vet ./... && go test ./...` 通过
- [ ] 网页控制台：`cd frontend && npm run typecheck && npm run build` 通过
- [ ] 桌面客户端（如涉及）：`cd client && npm run typecheck && npm run build:main && npm run build:renderer` 通过
- [ ] 改了 Go 核心：已执行 `node client/scripts/build-core.mjs` 重编 `codeporter-core.exe`
- [ ] 涉及 HTTP 接口：已同步更新 [docs/api.md](../blob/master/docs/api.md)
- [ ] 涉及配置项 / 环境变量：已同步更新 `configs/*.yaml`、`.env.example` 与相关文档
- [ ] 没有在提交中包含秘钥、App Secret、本地截图等敏感或临时文件

## 截图 / 演示（可选）

<!-- UI 变更建议附上前后对比截图 -->

## 额外说明（可选）

<!-- 破坏性变更、迁移步骤、需要 reviewer 特别关注的点 -->

# 古籍修复纸张适配放行台

面向古籍修复实验室的纸张适配资格管理 HTTP 服务，覆盖建档、方案、条件化、双盲样检测、差异复测、独立放行及证据封存，并提供可审计的本地事件日志。

## 构建与运行

```bash
go build ./...
go run ./cmd/server -addr=127.0.0.1:19081
go test ./...
go run ./cmd/server -self-check -addr=127.0.0.1:19081
```

服务也可通过 `PORT` 环境变量指定端口，始终绑定 `127.0.0.1`。API 根路径为 `/api/v1/qualification-cases`，写请求需携带 `request_id` 与 `expected_revision`。

## 主要查询与操作

- `GET /api/v1/qualification-cases`：支持 `created_from`、`created_to`、`stale_minutes`、`status`、`purpose`、`owner_id`、`candidate_batch_code`、`page` 和 `page_size`，返回分页前总数及状态积压统计。
- `POST /api/v1/qualification-cases/{id}/plan`：传入 `validate_only=true` 可在不改变状态和 revision 的前提下取得逐字段校验报告；带 `request_id` 的报告可跨重启幂等读取。
- `POST /api/v1/qualification-cases/{id}/conditioning`：除兼容单条读数外，可传入最多 500 条 `readings`。整批会校验时间顺序、重复、重叠、缺口和方案范围；`validate_only=true` 返回合并后的累计暴露、最长连续窗口及可确认状态且不推进 revision，正式请求可同时传入 `confirm=true` 原子确认。
- `GET /api/v1/qualification-cases/{id}/conditioning/summary`：返回读数合规性、缺口、累计暴露和最长连续窗口。单独确认仍向原 `conditioning` 写端点传入 `confirm=true`，并可携带摘要中的 `window_from` 与 `window_to`。
- `GET /api/v1/qualification-cases/{id}/measurements/report`：返回按指标排序的双组归一化对照、原始单位、阈值余量、差异 ID 和事件链绑定信息。
- `GET /api/v1/qualification-cases/{id}/discrepancies`：返回按指标排序的复测任务、待处理 ID 和完成百分比；原写端点同时接受单项字段或 `items` 批量裁决。
- `GET /api/v1/qualification-cases/{id}/release`：取得前置检查、整改进度和绑定当前 revision 的 `snapshot_hash`。驳回会建立整改轮次；随后通过原 `measurements` 端点提交完整双组替换测量，并携带 `remediation_note` 与 `supersedes_revision`，完成既有差异裁决后使用新快照再次签署。
- `GET /api/v1/qualification-cases/{id}/manifest`：常规模式支持 `page`、`page_size` 和 `include_entries`。对已封存个案还可传入 `evidence_stage`（`filing`、`plan`、`conditioning`、`measurement`、`retest`、`release`、`seal`）、`from_revision`、`to_revision` 以及三个 `expected_*` 摘要，取得与分页无关的 `selection_sha256`、逐项 SHA-256 和终态绑定核验结果。
- `GET /api/v1/qualification-cases/{id}/audit`：支持 `event_type`、`request_id`、`from_revision`、`to_revision`、`page` 和 `page_size`，返回稳定事件页与区间首尾哈希证明。

封存后的个案只返回安全摘要，所有写命令均会被拒绝；清单、核验结果、幂等命令结果和审计分页在事件回放后保持一致。

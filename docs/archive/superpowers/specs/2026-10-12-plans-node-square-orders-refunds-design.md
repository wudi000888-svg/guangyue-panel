# 套餐节点权限、节点广场与订单售后重构设计

## 目标

本轮允许破坏性迁移，直接把套餐、节点展示、订单售后和工单入口收敛为可验证的状态模型：

- 套餐仍是唯一访问授权来源；套餐内先选节点组，再对组内节点做独立选择。
- 套餐可对选中的节点设置独立流量倍率，覆盖节点默认倍率且只对该套餐生效。
- 新增会员可见的节点广场，只展示管理员公开、启用且可被至少一个已上架套餐使用的节点，并列出对应套餐。
- 管理员套餐不可删除；演示套餐可删除，删除状态持久化，不在重启时补种。
- 退款申请、用户撤销、管理员拒绝和管理员批准使用明确动作与 CAS 校验，拒绝/撤销不改变权益和资金。
- 订单和工单沿用现有成熟的分页、权限、幂等和附件能力，统一动作反馈与状态过滤，避免重复实现第二套系统。

## 数据模型

`Plan` 增加 `node_rate_overrides`，键使用节点组成员的 canonical selection key，值为整数 milli 倍率。`Entitlement` 与 `PlanSlot` 保存同一份快照，套餐更新只同步当前生效权益的节点授权和倍率，不重算历史用量。计量和订阅渲染按 `override -> node default` 取有效倍率。

节点增加 `public_display`、`public_name`、`public_description` 字段；节点广场接口只返回名称、协议、国家、质量和套餐摘要，不返回出口地址、凭据、私有 IP 或订阅 URI。

退款申请保留订单履约状态，增加独立 `commerce_refund_requests` 聚合（requested/cancelled/rejected/approved）。批准后才进入原有退款履约和支付退款队列；取消或拒绝记录原因、操作人和时间。

## API 与界面

- `GET /api/node-square`：会员可读，服务端过滤公开/启用节点并关联 enabled offer。
- `POST /api/plans`：保存倍率覆盖并返回规范化套餐；订阅接口返回实时节点权限。
- `/node-square`：会员导航入口，卡片显示节点协议、地区、状态和可用套餐。
- `/orders/action`：新增 `cancel_refund`、`reject_refund`，统一 operation id、权限和状态校验；订单列表增加退款筛选状态。
- 工单保留 `/api/support/*`，补统一错误反馈、状态动作和订单关联展示，不迁移附件权限模型。

## 迁移与兼容

新增迁移文件，不修改已发布迁移；允许清理旧独立节点授权数据。演示套餐删除写入 tombstone，管理员套餐由服务端强制保护。017 迁移按订单快照把旧 `refund_requested` 状态回填为 `commerce_refund_requests.requested`，将订单履约状态规范化为 `completed`，从而把审核状态与权益状态分开。

## 验证

- Go：套餐倍率 round-trip、不同套餐计量隔离、节点广场字段过滤、退款动作权限/CAS、订单/工单分页与幂等。
- 前端：套餐编辑关闭、倍率选择、节点广场导航/空态、退款撤销/拒绝和主题静态扫描。
- 发布前执行 `go test ./...`、`npm test`、`vue-tsc --noEmit`、i18n 检查、Vite 构建和 release 检查。

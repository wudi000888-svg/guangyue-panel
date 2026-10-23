# 退款权益与加密货币模块 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让后续套餐变化不再阻断安全退款，并把加密货币支付、钱包和归集拆成独立管理模块。

**Architecture:** 以订单来源 ID 绑定当前权益和排队槽位，退款只撤销目标订单贡献并在事务内保持队列顺序；以独立前端路由容器组织现有加密 API，普通交易设置只保留法币配置和跳转入口。

**Tech Stack:** Go、SQLite/PostgreSQL、Vue 3、TypeScript、Vitest、现有 crypto invoice/wallet/sweep API。

**Spec:** `docs/archive/superpowers/specs/2026-10-12-refund-crypto-modules-design.md`

## Global Constraints

- 退款按订单剩余权益退款，已消耗流量不退。
- `AppliedRevision` 只用于审计，不作为退款资格硬阻断。
- 加密货币链上退款不得复用法币退款记录或给 CNY 钱包入账。
- 每个 mutating API 保持 `operation_id` 幂等和管理员重认证。
- 不破坏现有订单支付、地址永久保存、链上确认和归集 API。

---

### Task 1: 建立订单来源标识和退款队列回归测试

**Files:**
- Modify: `backend/internal/domain/entitlement.go`
- Modify: `backend/internal/controlplane/commerce_orders.go`
- Test: `backend/internal/controlplane/plan_queue_test.go`
- Test: `backend/internal/controlplane/payment_integration_test.go`

**Interfaces:**
- `domain.Entitlement.SourceOrderID string` and `domain.PlanSlot.SourceOrderID string` are optional JSON fields.
- `assignPlan` and `pauseCurrentPlan` accept the source order ID when called from commerce fulfillment; manual entitlement changes pass an empty ID.
- Refund tests use existing order APIs and assert the user record plus order state after `commerceWork`.

- [ ] **Step 1: Write failing backend tests**

Add tests for: editing a plan after order fulfillment still permits `request_refund`; A→B→C refunding the active C restores B, refunding queued B leaves A/C ordering intact, and refunding the oldest queued order leaves the active plan unchanged. Assert `SourceOrderID` values and that a refund never restores a strategy revision over a newer plan policy.

- [ ] **Step 2: Run the focused tests and verify the expected failures**

Run:

```bash
go test ./internal/controlplane -run 'Test(PlanStrategyChangeStillAllowsRefund|Refund.*PlanQueue)' -count=1
```

Expected: the new tests fail because the current revision equality guard rejects the refund and queue slots have no source order ID.

- [ ] **Step 3: Add the source IDs without changing refund behavior yet**

Add JSON fields and thread the order ID through purchase fulfillment and `pauseCurrentPlan`, leaving manual assignments empty. Keep old serialized records valid because empty strings are accepted.

- [ ] **Step 4: Run the focused tests again**

Run the same command and confirm failures now reach the refund eligibility/queue assertions rather than compile or decode errors.

- [ ] **Step 5: Commit the model/test setup**

```bash
git add backend/internal/domain/entitlement.go backend/internal/controlplane/commerce_orders.go backend/internal/controlplane/plan_queue_test.go backend/internal/controlplane/payment_integration_test.go
git commit -m "test: identify entitlements by source order"
```

### Task 2: Implement transactional refunds that preserve entitlement order

**Files:**
- Modify: `backend/internal/controlplane/commerce_orders.go`
- Modify: `backend/internal/controlplane/node_policy.go`
- Modify: `backend/internal/controlplane/entitlement_cycles.go`
- Modify: `backend/internal/controlplane/commerce_types.go`
- Test: `backend/internal/controlplane/plan_queue_test.go`
- Test: `backend/internal/controlplane/payment_integration_test.go`

**Interfaces:**
- `refundOrderEntitlement(tx, order, user, now)` revokes the matching active/queued source order and returns whether a remote revocation barrier is required.
- `removeQueuedPlanSlot(user, sourceOrderID)` removes only the matching slot and preserves relative order of all other slots.
- `remainingRefundableAmount(order, user)` returns the order amount reduced by consumed quota, never below zero.

- [ ] **Step 1: Implement group-safe refund eligibility**

Remove `u.Entitlement.Revision != o.AppliedRevision` as a hard rejection for `request_refund` and administrator `refund`. Require an exact source order ID for automatic entitlement changes; if missing, return the explicit manual reconciliation error.

- [ ] **Step 2: Implement queued-slot removal**

When the target source ID is in `PlanQueue`, remove that slot in the same transaction, preserve all other slots and their `PausedAt`/`Expires`, mark the order `refunded` only after the payment channel confirms, and do not set `Meter.PendingReset`.

- [ ] **Step 3: Implement active-slot revocation and restoration**

When the target source ID is active, keep the existing remote revoke barrier but perform the final user/order update in one transaction. Restore the most recent non-refunded queue slot after the barrier; keep the newer plan policy fields from the slot’s plan snapshot while allowing current node policy synchronization to update the active revision.

- [ ] **Step 4: Handle renewals and refundable amount**

Record the renewal source ID and previous expiry. Refund only the unused extension, leaving later purchases and manual changes untouched. Reuse existing money posting and provider refund states; do not mark external refunds successful before the provider callback or chain transfer evidence.

- [ ] **Step 5: Add CAS and idempotency checks**

Reload the order and user inside the transaction, compare order state, refund request revision, source order ID and user document version, then update both documents. Replayed operation IDs return the original response; concurrent approvals return a conflict without a second money entry.

- [ ] **Step 6: Run focused and full backend tests**

```bash
go test -race ./internal/controlplane -run 'Test(PlanStrategyChangeStillAllowsRefund|Refund.*PlanQueue|Payment.*Refund)' -count=1
go test -race ./... -count=1
go vet ./...
```

Expected: all tests pass with no duplicate refund entries and no queue reordering.

- [ ] **Step 7: Commit backend behavior**

```bash
git add backend/internal/domain/entitlement.go backend/internal/controlplane/commerce_orders.go backend/internal/controlplane/node_policy.go backend/internal/controlplane/entitlement_cycles.go backend/internal/controlplane/commerce_types.go backend/internal/controlplane/plan_queue_test.go backend/internal/controlplane/payment_integration_test.go
git commit -m "fix: refund order entitlements without disturbing queue"
```

### Task 3: Split the cryptocurrency administration UI

**Files:**
- Create: `frontend/src/pages/CryptoPaymentsPage.vue`
- Modify: `frontend/src/pages/SettingsPage.vue`
- Modify: `frontend/src/composables/usePanel.ts`
- Modify: `frontend/src/router.ts`
- Modify: `frontend/src/components/CommerceSettings.vue`
- Test: `frontend/src/lib/cryptoModule.test.ts`

**Interfaces:**
- The route `/crypto-payments` renders `CryptoPaymentsPage` for owners only.
- `CryptoPaymentsPage` exposes workspaces `payments`, `wallets`, and `treasury`, reusing `CryptoPaymentSettings`, `CryptoWalletSettings`, and `CryptoTreasury` with their existing API contracts.
- Commerce settings emits a link to `/crypto-payments` and no longer renders wallet/crypto treasury panels inline.

- [ ] **Step 1: Write failing navigation and composition tests**

Test that an owner sees the crypto sidebar entry and route, that the page renders the three workspace labels, and that `CommerceSettings` no longer mounts `CryptoWalletSettings` or `CryptoTreasury` directly.

- [ ] **Step 2: Run the focused Vitest file and verify failure**

```bash
node node_modules/vitest/vitest.mjs run src/lib/cryptoModule.test.ts
```

Expected: the route/page and new entry are missing.

- [ ] **Step 3: Implement the independent module shell**

Add the route and sidebar item, build the page header/status strip, workspace tabs, responsive cards, and explicit links from ordinary transaction settings. Keep user checkout components unchanged.

- [ ] **Step 4: Remove duplicated crypto panels from commerce settings**

Leave only fiat provider configuration and a clear “打开加密货币模块” card. Preserve existing settings API calls and permissions.

- [ ] **Step 5: Run focused frontend tests and static checks**

```bash
node node_modules/vitest/vitest.mjs run src/lib/cryptoModule.test.ts
node node_modules/vue-tsc/bin/vue-tsc.js --noEmit
```

- [ ] **Step 6: Commit the independent crypto module**

```bash
git add frontend/src/pages/CryptoPaymentsPage.vue frontend/src/pages/SettingsPage.vue frontend/src/composables/usePanel.ts frontend/src/router.ts frontend/src/components/CommerceSettings.vue frontend/src/lib/cryptoModule.test.ts
git commit -m "feat: separate cryptocurrency administration module"
```

### Task 4: Full verification and release

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `VERSION` (generated by the repository release workflow)

- [ ] **Step 1: Run the complete frontend checks**

```bash
node check-i18n.mjs
node node_modules/vitest/vitest.mjs run
node node_modules/vue-tsc/bin/vue-tsc.js --noEmit
node node_modules/vite/bin/vite.js build
```

- [ ] **Step 2: Review diff and security boundaries**

Run `git diff --check`, inspect refund SQL for state/CAS predicates, verify crypto UI does not expose secrets, and confirm no temporary dependency links are tracked.

- [ ] **Step 3: Push, open PR, and wait for CI**

Push `codex/refund-crypto-modules`, create a PR to `main`, and wait for Go race/vet, frontend build, security scans, and Lite/Pro installation checks.

- [ ] **Step 4: Merge and verify the generated release**

After all checks pass, merge the PR and verify the automatic release contains Lite, Pro, `SHA256SUMS`, and `SBOM.spdx.json` assets. Report the release URL and download links.

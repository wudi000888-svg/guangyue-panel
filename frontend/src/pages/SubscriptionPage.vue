<script setup lang="ts">
import { usePanelContext } from "../composables/panelContext";
const { state, busy, sub, subUser, format, subProtocol, qr, qrError, subLoading, subError, simpleMode, publicSubPage, owner, userStatus, subURL, bytes, date, go, confirmUser, loadSub, copy, downloadSub } = usePanelContext();
import { Copy, Download, KeyRound, LoaderCircle, QrCode, RefreshCw } from "lucide-vue-next";
import { t } from "../i18n";
import NodeSubscriptionCards from "../NodeSubscriptionCards.vue";
</script>
<template>
<section v-if="state">
          <div class="page-heading">
            <div>
              <div class="eyebrow">SUBSCRIPTION</div>
              <h1>
                {{ publicSubPage ? t("公共订阅") : t("普通订阅") }}
              </h1>
            </div>
            <select v-if="owner" v-model="subUser" :aria-label="t('订阅成员')">
              <option v-for="u in state.users" :key="u.id" :value="u.id">
                {{ u.username }}
              </option>
            </select>
          </div>
          <div class="pool-intro"><QrCode :size="21"/><div><strong>{{publicSubPage ? t('公共节点专用订阅') : t('普通节点订阅')}}</strong><p>{{publicSubPage ? t('独立订阅凭据，仅含公共节点；拉黑或剔除后自动更新。公共池为空时，Mihomo 配置使用 REJECT，不回退直连。') : simpleMode ? t('获取已授权的节点配置，将订阅导入客户端即可连接。') : t('仅含普通节点，公共节点使用独立公共订阅地址。')}}</p></div><span v-if="!simpleMode" class="spacer"/><button v-if="!simpleMode" @click="go(publicSubPage ? 'subscription' : 'public-subscription')">{{publicSubPage ? t('普通订阅') : t('公共订阅')}}</button></div>
          <p v-if="subError" class="error" role="alert">{{ t(subError) }} <button :disabled="subLoading" @click="loadSub(true)"><RefreshCw :size="14"/>{{t('重试')}}</button></p>
          <div v-if="subLoading && !sub" class="empty" role="status"><LoaderCircle :size="20" class="spin"/> {{t('正在读取订阅节点…')}}</div>
          <template v-if="sub"
            ><div class="sub-summary">
              <div class="user-cell">
                <span class="avatar">{{
                  sub.user.username.slice(0, 1).toUpperCase()
                }}</span>
                <div>
                  <strong>{{ sub.user.username }}</strong
                  ><small>{{
                    sub.user.role === "owner" ? t("管理员") : t("企业成员")
                  }}</small>
                </div>
              </div>
              <span :class="['badge', sub.active ? 'success' : 'danger']">{{
                userStatus(sub.user)
              }}</span
              ><span class="spacer"></span>
              <div>
                <small>{{ t("已用流量") }}</small
                ><strong
                  >{{ bytes(sub.user.upload + sub.user.download)
                  }}<em>
                    / {{ sub.user.quota ? bytes(sub.user.quota) : t("不限") }}</em
                  ></strong
                >
              </div>
              <div>
                <small>{{ t("到期时间") }}</small
                ><strong>{{ date(sub.user.expires) }}</strong>
              </div>
            </div>
            <div class="section-head">
              <h2>{{ t("订阅配置") }}</h2>
              <span class="muted">{{
                (state.runtime?.mode === 'no_logs' || state.runtime?.subscription_access_enabled === false)
                  ? (sub.user.last_sub ? t("最近记录 ") + date(sub.user.last_sub, true) + ' · ' : '') + t("访问时间已停止记录")
                  : sub.user.last_sub
                  ? t("最近访问 ") + date(sub.user.last_sub, true)
                  : t("尚未访问")
              }}</span>
            </div>
            <div class="subscription-layout">
              <div class="subscription-controls">
                <div class="segmented">
                  <button
                    v-for="f in [
                      { id: 'mihomo', name: 'Mihomo' },
                      { id: 'base64', name: 'Base64' },
                      { id: 'raw', name: t('原始链接') },
                    ]"
                    :key="f.id"
                    :class="{ selected: format === f.id }"
                    @click="format = f.id"
                  >
                    {{ f.name }}
                  </button>
                </div>
                <label
                  >{{ t("协议") }}<select v-model="subProtocol">
                    <option value="">{{ t("全部协议") }}</option>
                    <option value="vless">VLESS</option>
                    <option value="hy2">Hysteria2</option>
                  </select></label
                ><label
                  >{{ t("订阅地址") }}<div class="copy-field">
                    <input
                      :value="subURL"
                      readonly
                      :aria-label="t('订阅地址')"
                      @focus="($event.target as HTMLInputElement).select()"
                    /><button
                      class="icon"
                      :title="t('复制订阅地址')"
                      @click="copy(subURL)"
                    >
                      <Copy :size="17" />
                    </button></div
                ></label>
                <div class="button-row">
                  <button
                    class="primary"
                    :disabled="!sub.active || busy"
                    @click="downloadSub"
                  >
                    <Download :size="17" />{{ t("下载订阅") }}</button
                  ><button @click="copy(subURL)">
                    <Copy :size="17" />{{ t("复制地址") }}</button>
                </div>
              </div>
              <div class="qr-area">
                <img
                  v-if="qr"
                  :src="qr"
                  width="220"
                  height="220"
                  :alt="t('当前订阅地址二维码')"
                /><p v-else-if="qrError" class="error" role="alert">{{qrError}}</p><span
                  >{{
                    format === "mihomo"
                      ? "Mihomo"
                      : format === "base64"
                        ? "Base64"
                        : "URI"
                  }}{{ t("订阅") }}</span
                >
              </div>
            </div>
            <NodeSubscriptionCards :key="sub.user.id + ':' + sub.pool + ':' + subProtocol" :nodes="sub.nodes" :active="sub.active" :pool="sub.pool" :loading="subLoading" @refresh="loadSub()"/>
            <div v-if="owner" class="subscription-security">
              <h2>{{ t("凭据管理") }}</h2>
              <button @click="confirmUser(sub.user, publicSubPage ? 'rotate-public-sub' : 'rotate-sub')">
                <RefreshCw :size="16" />{{ t("重置订阅地址") }}</button
              ><button
                class="danger-button"
                @click="confirmUser(sub.user, 'revoke')"
              >
                <KeyRound :size="16" />{{ t("撤销全部旧配置") }}</button>
            </div></template
          >
        </section>
</template>

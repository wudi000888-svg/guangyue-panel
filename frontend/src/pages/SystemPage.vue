<script setup lang="ts">
import { usePanelContext } from "../composables/panelContext";
const { state, selectedSite, bytes, date, duration, backup, actionName } = usePanelContext();
import { Download, Server, Settings2 } from "lucide-vue-next";
import { t } from "../i18n";
</script>
<template>
<section v-if="state">
          <div class="page-heading">
            <div>
              <div class="eyebrow">SYSTEM</div>
              <h1>{{ t("运维状态") }}</h1>
            </div>
            <button v-if="!selectedSite" @click="backup"><Download :size="17" />{{ t("下载备份") }}</button>
          </div>
          <div class="metrics system-metrics">
            <div>
              <span>{{ t("控制程序内存堆") }}</span
              ><strong>{{ bytes(state.system.heap) }}</strong
              ><small>Go HeapAlloc</small>
            </div>
            <div>
              <span>{{ t("控制程序运行时间") }}</span
              ><strong>{{ duration(state.system.uptime) }}</strong
              ><small>{{ state.system.version }}</small>
            </div>
            <div>
              <span>{{ t("证书有效期") }}</span
              ><strong
                >{{
                  Math.max(
                    0,
                    Math.ceil(
                      (state.system.cert_expires * 1000 - Date.now()) /
                        86400000,
                    ),
                  )
                }}<em>{{ t("天") }}</em></strong
              ><small>{{ date(state.system.cert_expires) }}</small>
            </div>
          </div>
          <div class="section-head">
            <h2>{{ t("运行服务") }}</h2>
            <span
              :class="[
                'badge',
                state.system.status === 'applied' ? 'success' : 'danger',
              ]"
              >{{
                state.system.status === "applied"
                  ? t("已应用")
                  : state.system.status === "pending"
                    ? t("待应用")
                    : t("异常")
              }}</span
            >
          </div>
          <div class="service-list">
            <div>
              <span class="service-symbol"><Settings2 :size="18" /></span
              ><strong>{{ t("广月企业控制台") }}</strong
              ><span class="muted">{{ t("成员与订阅管理") }}</span
              ><span class="badge success">{{ t("运行中") }}</span>
            </div>
            <div v-for="s in state.system.services" :key="s.name">
              <span class="service-symbol"><Server :size="18" /></span
              ><strong>{{ s.name }}</strong
              ><span class="muted">{{
                s.name === "Xray"
                  ? "VLESS Reality · TCP 443"
                  : s.name === "Hysteria2"
                    ? "HY2 · UDP 443"
                    : "HTTPS / HTTP/2 · SNI"
              }}</span
              ><span :class="['badge', s.active ? 'success' : 'danger']">{{
                s.active ? t("运行中") : t("未运行")
              }}</span>
            </div>
          </div>
          <p v-if="state.system.traffic_error" class="error">
            {{ state.system.traffic_error }}
          </p>
          <div class="section-head">
            <h2>{{ t("最近操作") }}</h2>
            <span class="muted">{{ t("最近 30 条") }}</span>
          </div>
          <div class="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>{{ t("时间") }}</th>
                  <th>{{ t("操作") }}</th>
                  <th>{{ t("对象") }}</th>
                  <th>{{ t("操作人") }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(a, i) in state.audit" :key="i">
                  <td class="muted">{{ date(a.at, true) }}</td>
                  <td>{{ actionName(a.action) }}</td>
                  <td>{{ a.target }}</td>
                  <td>{{ a.actor }}</td>
                </tr>
                <tr v-if="!state.audit.length">
                  <td colspan="4" class="empty">{{ t("暂无记录") }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </section>
</template>

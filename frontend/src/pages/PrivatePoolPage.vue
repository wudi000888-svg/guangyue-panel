<script setup lang="ts">
import { usePanelContext } from "../composables/panelContext";
const { state, busy, speedRunning, qualityRunning, qualityTest, importReport, openImport, speedTest, ipSearch, ipType, ipState, privateTab, privateSourcesReady, privateSourcesLoading, privateSourcesError, receivePrivateSources, loadPrivateSources, isSubscribedResource, privateTabs, sourceLabel, selectPrivateTab, viewSourceResources, sourceChanged, poolStats, ipStatus, ipBadge, sourceFilter, filteredIPs, detecting, date, exitName, go, editIP, detectIP, deleteIP, selectedIPIDs, selectAll, batchAction } = usePanelContext();
import { ArrowUpRight, Download, Gauge, LoaderCircle, Database, Pencil, Plus, RefreshCw, Search, Trash2, X } from "lucide-vue-next";
import { t } from "../i18n";
import ImportSources from "../ImportSources.vue";
import ResourceTabs from "../ResourceTabs.vue";
import CountryMark from "../CountryMark.vue";
import SpeedMetric from "../SpeedMetric.vue";
import QualityTags from "../QualityTags.vue";
import { usePagination } from "../composables/usePagination";
import ListTable from "../components/ListTable.vue";
const {page:listPage,pages:listPages,rows:listRows}=usePagination(filteredIPs);
</script>
<template>
<section v-if="state" class="private-pool-page">
          <div class="page-heading">
            <div><h1>{{ t("私有 IP 池") }}</h1><p class="resource-page-description">{{t("按来源管理企业出口，统一检测与分配")}}</p></div>
            <div class="heading-actions">
              <button @click="openImport()">
                <Download :size="17" />{{ t("导入订阅 / 节点") }}</button
              ><button class="primary" @click="editIP()">
                <Plus :size="17" />{{ t("添加 IP 资源") }}</button>
            </div>
          </div>
          <div class="user-stat-grid">
            <div>
              <span>{{ t("IP 池资源") }}</span><strong>{{ poolStats.total }}</strong>
            </div>
            <div>
              <span>{{ t("检测可用") }}</span><strong>{{ poolStats.ready }}</strong>
            </div>
            <div>
              <span>{{ t("已分配") }}</span><strong>{{ poolStats.used }}</strong>
            </div>
            <div>
              <span>{{ t("未分配") }}</span
              ><strong>{{ poolStats.total - poolStats.used }}</strong>
            </div>
          </div>
          <ResourceTabs id="private-pool" :model-value="privateTab" :items="privateTabs" :label="t('私有 IP 池分类')" @update:model-value="selectPrivateTab"/>
          <div id="private-pool-panel" role="tabpanel" :aria-labelledby="'private-pool-tab-' + privateTab">
          <ImportSources v-if="privateTab === 'sources'" embedded @refresh="sourceChanged" @resources="viewSourceResources" @loaded="receivePrivateSources"/>
          <div v-else-if="!privateSourcesReady" class="resource-membership-state" :aria-busy="privateSourcesLoading" role="status"><LoaderCircle v-if="privateSourcesLoading" :size="23" class="spin"/><Database v-else :size="23"/><h2>{{privateSourcesLoading ? t('正在确认出口来源…') : t('暂时无法确认出口分类')}}</h2><p>{{privateSourcesLoading ? t('读取订阅关联后展示出口分类，避免将订阅出口误列为独立出口。') : t(privateSourcesError)}}</p><button v-if="!privateSourcesLoading" @click="loadPrivateSources"><RefreshCw :size="14"/>{{t('重新读取来源')}}</button><button class="text-button" @click="selectPrivateTab('sources')">{{t('管理订阅来源')}}</button></div>
          <template v-else>
          <div v-if="privateSourcesError" class="resource-membership-notice" role="status"><span>{{t('来源信息刷新失败，当前保留上次确认的分类。')}}</span><button class="text-button" :disabled="privateSourcesLoading" @click="loadPrivateSources"><RefreshCw :size="13" :class="{spin:privateSourcesLoading}"/>{{t('重试')}}</button></div>
          <div class="resource-section-heading"><div><h2>{{privateTab === 'subscriptions' ? t('订阅出口') : t('独立出口')}}</h2><p>{{privateTab === 'subscriptions' ? t('由已管理的上游订阅同步，更新时保留节点绑定') : t('单独添加或一次性导入的出口，由你独立维护')}}</p></div><button class="text-button" @click="go('nodes')">{{t('分配至节点')}}<ArrowUpRight :size="15"/></button></div>
          <div v-if="sourceFilter" class="resource-source-filter"><Database :size="15"/><strong>{{sourceFilter.name}}</strong><button class="text-button" @click="sourceFilter=null"><X :size="13"/>{{t('清除来源筛选')}}</button></div>
          <div v-if="importReport && !importReport.queued && privateTab === 'manual'" class="import-report" role="status">
            <div>
              <strong>{{ t("独立出口导入结果") }}</strong>
              <p>{{ t("新增") }} {{ importReport.added }} {{ t("项 · 重复跳过") }} {{ importReport.duplicates }} {{ t("项 · 错误跳过") }} {{ importReport.warnings.length }} {{ t("项") }}</p>
              <details v-if="importReport.warnings.length">
                <summary>{{ t("查看跳过原因") }}</summary>
                <p v-for="w in importReport.warnings" :key="w.index">{{ t("行 / 项") }} {{ w.index }} · {{ t(w.reason) }}
                </p>
              </details>
            </div>
            <button
              class="icon"
              :title="t('关闭导入结果')"
              @click="importReport = null"
            >
              <X :size="16" />
            </button>
          </div>
          <div class="toolbar pool-toolbar">
            <div class="search">
              <Search :size="17" /><input
                v-model="ipSearch"
                :placeholder="t('搜索国家、IP、备注或代理地址')"
                :aria-label="t('搜索 IP')"
              />
            </div>
            <select v-model="ipType" :aria-label="t('IP 出口类型')">
              <option value="all">{{ t("全部类型") }}</option>
              <option value="subscription">{{ t("机场节点") }}</option>

              <option value="http">HTTP CONNECT</option>
              <option value="socks5">SOCKS5</option></select
            ><select v-model="ipState" :aria-label="t('IP 资源状态')">
              <option value="all">{{ t("全部状态") }}</option>
              <option value="ready">{{ t("检测可用") }}</option>
              <option value="attention">{{ t("待检测 / 异常") }}</option>
              <option value="unassigned">{{ t("未分配") }}</option>
              <option value="disabled">{{ t("已停用") }}</option></select
            ><span class="spacer"></span
            ><span class="muted">{{ filteredIPs.length }}{{ t("项资源") }}</span>
          </div>
          <div v-if="selectedIPIDs.length" class="batch-toolbar" role="region" :aria-label="t('批量操作')">
            <strong>{{t('已选择')}} {{selectedIPIDs.length}}</strong>
            <button :disabled="busy" @click="batchAction('ips','enable')">{{t('批量启用')}}</button>
            <button :disabled="busy" @click="batchAction('ips','disable')">{{t('批量停用')}}</button>
            <button v-if="privateTab === 'manual'" class="danger-button" :disabled="busy" @click="batchAction('ips','delete')"><Trash2 :size="14"/>{{t('批量删除')}}</button>
            <button class="text-button" :disabled="busy" @click="selectedIPIDs=[]">{{t('清空选择')}}</button>
          </div>
          <ListTable :total="filteredIPs.length" v-model:page="listPage" :pages="listPages">
            <table>
              <thead>
                <tr>
                  <th class="select-cell"><input type="checkbox" :aria-label="t('选择全部当前结果')" :disabled="busy || !filteredIPs.length" :checked="!!filteredIPs.length && selectedIPIDs.length === filteredIPs.length" :indeterminate="!!selectedIPIDs.length && selectedIPIDs.length < filteredIPs.length" @change="selectAll('ips',$event)"/></th>
                  <th>{{ t("出口 IP / 国家") }}</th>
                  <th>{{ t("备注名称") }}</th>
                  <th>{{ t("出口类型 / 地址") }}</th>
                  <th>{{ t("检测状态") }}</th>
                  <th>{{ t("绑定节点") }}</th>
                  <th>{{ t("出口测速 · VPS") }}</th>
                  <th class="right">{{ t("操作") }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="p in listRows" :key="p.id">
                  <td class="select-cell"><input type="checkbox" v-model="selectedIPIDs" :value="p.id" :aria-label="t('选择')+' '+ p.name" :disabled="busy"/></td>
                  <td>
                    <div class="user-cell">
                      <CountryMark
                        :code="p.country_code"
                        :country="p.country"
                      />
                      <div>
                        <strong>{{ p.probe_ip || t("等待检测") }}</strong
                        ><small
                          >{{ p.country || t("国家待识别")
                          }}<span v-if="p.country_code">
                            · {{ p.country_code }}</span
                          ></small
                        >
                      </div>
                    </div>
                  </td>
                  <td>
                    <strong>{{ p.label || t("未命名资源") }}</strong
                    ><small class="table-detail pool-note" :title="p.notes">{{
                      p.notes || "—"
                    }}</small>
                    <small class="table-detail resource-origin">{{sourceLabel(p)}}</small>
                    <span v-if="p.source_stale" class="badge neutral">{{t('来源已移除')}}</span>
                    <QualityTags :value="p.quality" :name="p.label || p.name" :running="qualityRunning === p.id" @run="qualityTest(p, true)" />
                  </td>
                  <td>
                    <span class="badge neutral">{{
                      p.upstream_type?.toUpperCase() || exitName(p.exit)
                    }}</span
                    ><small class="table-detail mono">{{
                      p.exit === "direct"
                        ? t("VPS 本机出口")
                        : p.host + ":" + p.port
                    }}</small>
                  </td>
                  <td>
                    <span :class="['badge', ipBadge(p)]"
                      ><i :class="['dot', { warning: !p.reachable }]" />{{
                        ipStatus(p)
                      }}</span
                    ><small
                      v-if="p.probe_error"
                      class="table-warning"
                      :title="p.probe_error"
                      >{{
                        p.reachable ? t("地理信息待更新") : t("检测失败，请检查出口")
                      }}</small
                    >
                  </td>
                  <td>
                    <span
                      :class="[
                        'badge',
                        p.node_ids?.length ? 'neutral' : 'unassigned',
                      ]"
                      >{{ p.node_ids?.length || 0 }}{{ t("个节点") }}</span
                    ><small class="table-detail">{{
                      [
                        ...new Set(
                          state.nodes
                            .filter((n) => n.exit_id === p.id)
                            .map((n) => n.protocol.toUpperCase()),
                        ),
                      ].join(" · ") || t("可供分配")
                    }}</small>
                  </td>
                  <td>
                    <SpeedMetric
                      :result="p.speed"
                      :running="speedRunning === p.id"
                    />
                  </td>
                  <td>
                    <div class="row-actions">
                      <button
                        class="icon speed-action"
                        :title="t('测速 IP ') + (p.label || p.name)"
                        :disabled="busy || !p.enabled"
                        @click="speedTest(p, true)"
                      >
                        <Gauge :size="16" />
                      </button>
                      <button
                        class="icon"
                        :title="t('检测 IP ') + (p.label || p.name)"
                        :disabled="busy"
                        @click="detectIP(p)"
                      >
                        <RefreshCw
                          :size="16"
                          :class="{ spin: detecting === p.id }"
                        /></button
                      ><button
                        class="icon"
                        :title="t('编辑 IP ') + (p.label || p.name)"
                        @click="editIP(p)"
                      >
                        <Pencil :size="16" /></button
                      ><button
                        class="icon danger-button"
                        :title="
                          isSubscribedResource(p) ? t('订阅出口请通过订阅来源管理删除') : t('删除 IP ') + (p.label || p.name)
                        "
                        :disabled="busy || isSubscribedResource(p)"
                        @click="deleteIP(p)"
                      >
                        <Trash2 :size="16" />
                      </button>
                    </div>
                  </td>
                </tr>
                <tr v-if="!filteredIPs.length">
                  <td colspan="8" class="empty">{{ t("没有匹配的 IP 资源") }}</td>
                </tr>
              </tbody>
            </table>
          <template #mobile>
          <label class="mobile-list-select"><input type="checkbox" :aria-label="t('选择全部当前结果')" :disabled="busy || !filteredIPs.length" :checked="!!filteredIPs.length && selectedIPIDs.length === filteredIPs.length" :indeterminate="!!selectedIPIDs.length && selectedIPIDs.length < filteredIPs.length" @change="selectAll('ips',$event)"/>{{t('选择全部当前结果')}}</label>
          <div class="pool-cards">
            <article v-for="p in listRows" :key="p.id" class="node-card">
              <div class="node-card-head"><input class="row-select" type="checkbox" v-model="selectedIPIDs" :value="p.id" :aria-label="t('选择')+' '+ p.name" :disabled="busy"/>
                <CountryMark :code="p.country_code" :country="p.country" />
                <div>
                  <h2>{{ p.label || p.name }}</h2>
                  <small>{{ p.name }}</small><small class="resource-origin">{{sourceLabel(p)}}</small><span v-if="p.source_stale" class="badge neutral">{{t('来源已移除')}}</span>
                </div>
                <span class="spacer"></span
                ><button
                  class="icon"
                  :title="t('编辑 IP ') + (p.label || p.name)"
                  @click="editIP(p)"
                >
                  <Pencil :size="17" />
                </button>
              </div>
              <dl class="node-details">
                <div>
                  <dt>{{ t("出口方式") }}</dt>
                  <dd>{{ exitName(p.exit) }}</dd>
                </div>
                <div>
                  <dt>{{ t("代理地址") }}</dt>
                  <dd class="mono">
                    {{
                      p.exit === "direct" ? t("VPS 本机") : p.host + ":" + p.port
                    }}
                  </dd>
                </div>
                <div>
                  <dt>{{ t("检测状态") }}</dt>
                  <dd>
                    <span :class="['badge', ipBadge(p)]">{{
                      ipStatus(p)
                    }}</span>
                  </dd>
                </div>
                <div>
                  <dt>{{ t("绑定节点") }}</dt>
                  <dd>{{ p.node_ids?.length || 0 }}{{ t("个") }}</dd>
                </div>
              </dl>
              <QualityTags :value="p.quality" :name="p.label || p.name" :running="qualityRunning === p.id" @run="qualityTest(p, true)" />
              <div class="card-speed">
                <span>{{ t("出口测速 · VPS") }}</span
                ><SpeedMetric
                  :result="p.speed"
                  :running="speedRunning === p.id"
                /><button
                  class="text-button"
                  :disabled="busy || !p.enabled"
                  @click="speedTest(p, true)"
                >
                  <Gauge :size="15" />{{ t("测速") }}</button>
              </div>
              <p v-if="p.probe_error" class="node-warning">
                {{ p.probe_error }}
              </p>
              <div class="node-card-footer">
                <span>{{
                  p.checked_at ? date(p.checked_at, true) : t("尚未检测")
                }}</span
                ><button
                  class="text-button"
                  :disabled="busy"
                  @click="detectIP(p)"
                >
                  <RefreshCw :size="14" />{{ t("检测") }}</button
                ><button
                  class="icon danger-button"
                  :title="t('删除 IP 资源')"
                  :disabled="busy || isSubscribedResource(p)"
                  @click="deleteIP(p)"
                >
                  <Trash2 :size="15" />
                </button>
              </div>
            </article>
            <p v-if="!filteredIPs.length" class="empty">{{ t("没有匹配的 IP 资源") }}</p>
          </div>
          </template>
          </ListTable>
          <div class="table-caption">
            <span>{{ t("共") }}{{ filteredIPs.length }}{{ t("项资源") }}</span
            ><span>{{ t("测速：VPS 经所选出口 → Cloudflare · 每次最多 8 MiB") }}</span>
          </div>
          </template>
          </div>
        </section>
</template>

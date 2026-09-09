<script setup lang="ts">
import { usePanelContext } from "../composables/panelContext";
const { state, busy, speedRunning, qualityRunning, qualityTest, speedTest, nodePoolLabel, detecting, nodeSearch, nodeProtocol, nodeStatus, publicNodePage, filteredNodes, date, exitName, go, editNode, detectNode, selectedNodeIDs, selectableNodes, selectAll, batchAction } = usePanelContext();
import { ArrowUpRight, CircleHelp, Gauge, Globe2, Pencil, Plus, RefreshCw, Search, ShieldCheck, Trash2 } from "lucide-vue-next";
import { t } from "../i18n";
import CountryMark from "../CountryMark.vue";
import SpeedMetric from "../SpeedMetric.vue";
import QualityTags from "../QualityTags.vue";
import { usePagination } from "../composables/usePagination";
import ListTable from "../components/ListTable.vue";
const {page:listPage,pages:listPages,rows:listRows}=usePagination(filteredNodes);
</script>
<template>
<section v-if="state">
          <div class="page-heading">
            <div>
              <div class="eyebrow">ROUTING</div>
              <h1>{{publicNodePage ? t("公共节点") : t("普通节点")}}</h1>
            </div>
            <div v-if="!publicNodePage" class="node-create-actions">
              <button @click="editNode(undefined, 'vless')">
                <Plus :size="17" />{{ t("新建 VLESS 节点") }}</button
              ><button class="primary" @click="editNode(undefined, 'hy2')">
                <Plus :size="17" />{{ t("新建 HY2 节点") }}</button>
            </div>
          </div>
          <div v-if="publicNodePage" class="pool-intro"><Globe2 :size="21"/><div><strong>{{ t("公共节点独立维护") }}</strong><p>{{ t("节点由公共池自动创建、拉黑与剔除。普通节点和普通订阅保留在各自页面。") }}</p></div><span class="spacer"/><button @click="go('public')">{{ t("采集与黑名单") }}</button><button class="primary" @click="go('public-subscription')">{{ t("公共订阅") }}</button></div>
          <div class="endpoint-band">
            <span class="endpoint-icon"><Globe2 :size="23" /></span>
            <div>
              <strong>{{ t("公网入口") }}</strong>
              <div class="endpoint-domains">
                <span>VLESS · {{ state.system.vless_host }}</span
                ><span>HY2 · {{ state.system.hy2_host }}</span>
              </div>
            </div>
            <div class="endpoint-ports">
              <span><i class="dot" />TCP 443</span
              ><span><i class="dot blue" />UDP 443</span>
            </div>
            <span class="badge neutral">Nginx SNI</span>
          </div>
          <div class="toolbar node-toolbar">
            <div class="search">
              <Search :size="17" /><input
                v-model="nodeSearch"
                :placeholder="t('搜索国家、IP 或出口地址')"
                :aria-label="t('搜索节点')"
              />
            </div>
            <select v-model="nodeProtocol" :aria-label="t('节点协议')">
              <option value="all">{{ t("全部协议") }}</option>
              <option value="vless">VLESS</option>
              <option value="hy2">Hysteria2</option></select
            ><select v-model="nodeStatus" :aria-label="t('节点状态')">
              <option value="all">{{ t("全部状态") }}</option>
              <option value="enabled">{{ t("已启用") }}</option>
              <option value="disabled">{{ t("已停用") }}</option></select
            ><span class="spacer"></span
            ><span class="muted">{{ filteredNodes.length }}{{ t("个节点") }}</span>
          </div>
          <div v-if="selectedNodeIDs.length" class="batch-toolbar" role="region" :aria-label="t('批量操作')">
            <strong>{{t('已选择')}} {{selectedNodeIDs.length}}</strong>
            <button :disabled="busy" @click="batchAction('nodes','enable')">{{t('批量启用')}}</button>
            <button :disabled="busy" @click="batchAction('nodes','disable')">{{t('批量停用')}}</button>
            <button  class="danger-button" :disabled="busy" @click="batchAction('nodes','delete')"><Trash2 :size="14"/>{{t('批量删除')}}</button>
            <button class="text-button" :disabled="busy" @click="selectedNodeIDs=[]">{{t('清空选择')}}</button>
          </div>
          <ListTable :total="filteredNodes.length" v-model:page="listPage" :pages="listPages">
            <table>
              <thead>
                <tr>
                  <th class="select-cell"><input type="checkbox" :aria-label="t('选择全部当前结果')" :disabled="busy || !selectableNodes.length" :checked="!!selectableNodes.length && selectedNodeIDs.length === selectableNodes.length" :indeterminate="!!selectedNodeIDs.length && selectedNodeIDs.length < selectableNodes.length" @change="selectAll('nodes',$event)"/></th>
                  <th>{{ t("节点名称") }}</th>
                  <th>{{ t("协议") }}</th>
                  <th>{{ t("节点出口") }}</th>
                  <th>{{ t("公网 IP") }}</th>
                  <th>{{ t("状态") }}</th>
                  <th>{{ t("出口测速 · VPS") }}</th>
                  <th class="right">{{ t("操作") }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="n in listRows" :key="n.id">
                  <td class="select-cell"><input type="checkbox" v-model="selectedNodeIDs" :value="n.id" :aria-label="t('选择')+' '+ n.name" :disabled="busy || n.default_direct"/></td>
                  <td>
                    <div class="user-cell">
                      <CountryMark
                        :code="n.country_code"
                        :country="n.country"
                      />
                      <div>
                        <strong>{{ n.name }}</strong
                        ><small
                          >{{
                            n.protocol === "vless"
                              ? state.system.vless_host
                              : state.system.hy2_host
                          }}:443</small
                        >
                      </div>
                    </div>
                  </td>
                  <td>
                    <span :class="['tag', n.protocol]">{{
                      n.protocol === "hy2" ? "Hysteria2" : "VLESS Reality"
                    }}</span>
                    <small v-if="n.protocol === 'vless' && n.reality_sni" class="node-sni-label">SNI · {{n.reality_sni}}</small><small v-if="n.dns?.mode === 'secure'" class="node-sni-label">{{t('出口 DNS 防护')}} · {{n.dns.ipv6 === 'block' ? 'IPv4' : 'IPv4 / IPv6'}}</small>
                    <span v-if="n.default_direct" class="badge neutral default-direct-badge"><ShieldCheck :size="11"/>{{t('默认直连')}}</span>
                  </td>
                  <td>
                    <strong>{{ nodePoolLabel(n) }}</strong>
                    <small class="table-detail"
                      >{{ exitName(n.exit) }} ·
                      {{
                        n.exit === "direct"
                          ? t("服务器直连")
                          : n.host + ":" + n.port
                      }}</small
                    >
                  </td>
                  <td><span class="mono">{{ n.probe_ip || t("待检测") }}</span><QualityTags :value="n.quality" :name="n.name" :running="qualityRunning === n.id" @run="qualityTest(n)" /></td>
                  <td>
                    <span
                      :class="['badge', n.enabled ? 'success' : 'neutral']"
                      >{{ n.enabled ? t("已启用") : t("已停用") }}</span
                    ><small
                      v-if="n.probe_error"
                      class="table-warning"
                      :title="n.probe_error"
                      >{{ t("检测需重试") }}</small
                    >
                  </td>
                  <td>
                    <SpeedMetric
                      :result="n.speed"
                      :running="speedRunning === n.id"
                    />
                  </td>
                  <td>
                    <div class="row-actions">
                      <button
                        class="row-action-button speed-action"
                        :disabled="busy || !n.enabled"
                        @click="speedTest(n)"
                      >
                        <Gauge :size="15" />{{ t("测速") }}</button>
                      <button
                        class="row-action-button"
                        :disabled="busy"
                        :title="t('重新检测 ') + n.name"
                        @click="detectNode(n)"
                      >
                        <RefreshCw
                          :size="15"
                          :class="{ spin: detecting === n.id }"
                        />{{ t("检测") }}</button
                      ><button class="row-action-button" @click="n.managed_by ? go('public') : editNode(n)">
                        <Pencil :size="15" />{{ n.managed_by ? t('自动管理') : n.default_direct ? t('默认配置') : t('编辑') }}
                      </button>
                    </div>
                  </td>
                </tr>
                <tr v-if="!filteredNodes.length">
                  <td colspan="8" class="empty">{{ t("没有匹配的节点") }}</td>
                </tr>
              </tbody>
            </table>
          </ListTable>
          <div class="node-cards mobile-node-cards">
            <article v-for="n in listRows" :key="n.id" class="node-card">
              <div class="node-card-head"><input class="row-select" type="checkbox" v-model="selectedNodeIDs" :value="n.id" :aria-label="t('选择')+' '+ n.name" :disabled="busy || n.default_direct"/>
                <CountryMark :code="n.country_code" :country="n.country" />
                <div>
                  <h2>{{ n.name }}</h2>
                  <span :class="['tag', n.protocol]">{{
                    n.protocol === "vless"
                      ? "VLESS · Reality / TCP"
                      : "Hysteria2 · QUIC / UDP"
                  }}</span>
                  <span v-if="n.protocol === 'vless' && n.reality_sni" class="node-sni-label">SNI · {{n.reality_sni}}</span>
                  <span v-if="n.default_direct" class="badge neutral default-direct-badge"><ShieldCheck :size="11"/>{{t('默认直连')}}</span>
                </div>
                <span class="spacer"></span
                ><button class="icon" :title="n.managed_by ? t('自动节点策略') : n.default_direct ? t('默认配置') : t('编辑节点')" @click="n.managed_by ? go('public') : editNode(n)">
                  <Pencil :size="17" />
                </button>
              </div>
              <dl class="node-details">
                <div>
                  <dt>{{ t("状态") }}</dt>
                  <dd>
                    <span
                      :class="['badge', n.enabled ? 'success' : 'neutral']"
                      >{{ n.enabled ? t("已启用") : t("已停用") }}</span
                    >
                  </dd>
                </div>
                <div>
                  <dt>{{ t("当前出口") }}</dt>
                  <dd>{{ nodePoolLabel(n) }} · {{ exitName(n.exit) }}</dd>
                </div>
                <div>
                  <dt>{{ t("出口地址") }}</dt>
                  <dd>
                    {{
                      n.exit === "direct" ? t("VPS 本机") : n.host + ":" + n.port
                    }}
                  </dd>
                </div>
                <div>
                  <dt>{{ t("出口国家") }}</dt>
                  <dd>
                    {{ n.country || t("待识别")
                    }}<span v-if="n.country_code" class="country-code">{{
                      n.country_code
                    }}</span>
                  </dd>
                </div>
                <div>
                  <dt>{{ t("公网 IP") }}</dt>
                  <dd class="mono">{{ n.probe_ip || t("待检测") }}</dd>
                </div>
                <div>
                  <dt>{{ t("作用范围") }}</dt>
                  <dd>
                    {{ t("此逻辑节点") }}
                  </dd>
                </div>
              </dl>
              <QualityTags :value="n.quality" :name="n.name" :running="qualityRunning === n.id" @run="qualityTest(n)" />
              <div class="card-speed">
                <span>{{ t("出口测速 · VPS") }}</span
                ><SpeedMetric
                  :result="n.speed"
                  :running="speedRunning === n.id"
                /><button
                  class="text-button"
                  :disabled="busy || !n.enabled"
                  @click="speedTest(n)"
                >
                  <Gauge :size="15" />{{ t("测速") }}</button>
              </div>
              <p v-if="n.probe_error" class="node-warning">
                <CircleHelp :size="14" />{{ n.probe_error }}
              </p>
              <div class="node-card-footer">
                <span>{{
                  n.probed_at
                    ? date(n.probed_at, true) + t(" 已检测")
                    : t("尚无出口检测记录")
                }}</span
                ><button
                  class="icon"
                  :title="t('重新检测 ') + n.name"
                  :disabled="busy"
                  @click="detectNode(n)"
                >
                  <RefreshCw
                    :size="16"
                    :class="{ spin: detecting === n.id }"
                  /></button
                ><button class="text-button" @click="n.managed_by ? go('public') : editNode(n)">
                  {{ n.managed_by ? t('自动节点策略') : n.default_direct ? t('默认配置') : t('配置出口') }}<ArrowUpRight :size="15" />
                </button>
              </div>
            </article>
            <p v-if="!filteredNodes.length" class="empty">{{ t("没有匹配的节点") }}</p>
          </div>
          <div class="table-caption">
            <span>{{ t("共") }}{{ filteredNodes.length }}{{ t("个节点") }}</span
            ><span>{{ t("名称根据实际出口自动更新") }}</span>
          </div>
        </section>
</template>

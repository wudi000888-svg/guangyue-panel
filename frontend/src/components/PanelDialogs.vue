<script setup lang="ts">
import { computed, ref } from "vue";
import { useModalFocus } from "../composables/useModalFocus";
import { usePanelContext } from "../composables/panelContext";
const { state, busy, error, notice, modal, editingID, userForm, nodeForm, ipForm, importMode, importForm, importFile, importFileReading, importIssues, clearPrivateFile, readPrivateFile, importSubscription, ipPool, selectedIP, ipStatus, ipBadge, passwordForm, probeResult, displayedNodeName, confirmation, defaultRealitySNI, editingDefaultDirect, bytes, exitName, go, saveUser, confirmUser, confirmed, editNode, probe, saveIP, saveNode, deleteNode, changePassword } = usePanelContext();
import { Activity, ArrowUpRight, Check, CircleHelp, Download, Globe2, KeyRound, LoaderCircle, Plus, RefreshCw, ShieldCheck, Trash2, X } from "lucide-vue-next";
import { t } from "../i18n";
import CountryMark from "../CountryMark.vue";
const editDialog=ref<HTMLElement|null>(null), confirmDialog=ref<HTMLElement|null>(null);
useModalFocus(computed(()=>!!modal.value || !!confirmation.value), computed(()=>confirmation.value ? confirmDialog.value : editDialog.value), ()=>{
  if(busy.value)return;
  if(confirmation.value)confirmation.value=null;else modal.value='';
});
</script>
<template>
<div v-if="modal" class="modal-shade" @click.self="!busy && (modal = '')">
    <section
      class="modal"
      ref="editDialog" tabindex="-1" :inert="!!confirmation"
      role="dialog"
      aria-modal="true"
      :aria-label="
        modal === 'user'
          ? t('成员设置')
          : modal === 'ip'
            ? t('IP 资源设置')
            : modal === 'node'
              ? t('节点设置')
              : modal === 'import'
                ? t('导入订阅或节点')
                : t('修改密码')
      "
    >
      <div class="modal-head">
        <h2>
          {{
            modal === "user"
              ? editingID
                ? t("编辑成员")
                : t("添加成员")
              : modal === "ip"
                ? ipForm.id
                  ? t("编辑 IP 资源")
                  : t("添加 IP 资源")
                : modal === "node"
                  ? nodeForm.id
                    ? t("编辑节点")
                    : t("新建 ") + nodeForm.protocol.toUpperCase() + t(" 节点")
                  : modal === "import"
                    ? t("导入订阅或节点")
                    : t("修改密码")
          }}
        </h2>
        <button class="icon" :title="t('关闭')" :disabled="busy" @click="modal = ''">
          <X :size="20" />
        </button>
      </div>
      <form v-if="modal === 'import'" @submit.prevent="importSubscription">
        <div class="import-intro">
          <div class="import-symbol"><Download :size="24" /></div>
          <div>
            <strong>{{ t("将订阅或单个节点加入出口池") }}</strong>
            <p>{{ t("导入后，可供 VLESS 与 HY2 节点选择使用。") }}</p>
          </div>
        </div>
        <div class="import-tabs">
          <button
            type="button"
            :class="{ active: importMode === 'url' }"
            :disabled="busy || importFileReading"
            @click="importMode = 'url'"
          >{{ t("订阅 / 单节点") }}</button
          ><button
            type="button"
            :class="{ active: importMode === 'content' }"
            :disabled="busy || importFileReading"
            @click="importMode = 'content'"
          >{{ t("粘贴内容") }}</button><button type="button" :class="{ active: importMode === 'file' }" :disabled="busy || importFileReading" @click="importMode = 'file'"><Download :size="14"/>{{t("导入文件")}}</button>
        </div>
        <label v-if="importMode === 'url'"
          >{{ t("订阅或单节点链接") }}<input
            v-model="importForm.url"
            type="password"
            autocomplete="off"
            required
            :placeholder="t('https://… 或 vless://…')"
            :aria-label="t('订阅或单节点链接')"
          /><span class="field-caption"
            >{{ t("自动识别 HTTPS 订阅与 Shadowrocket / 普通节点链接。") }}</span
          ></label
        >
        <label v-else-if="importMode === 'content'"
          >{{ t("节点或订阅内容") }}<textarea
            v-model="importForm.content"
            rows="7"
            required
            maxlength="4194304"
            :placeholder="t('每行一个节点链接或 IP:端口:账号:密码，也支持 Clash / Mihomo 和 Base64')"
            :aria-label="t('订阅内容')"
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
          ></textarea>
        </label>
        <div v-else class="private-file-import" :aria-busy="importFileReading">
          <div class="private-file-heading"><Download :size="23"/><div><strong>{{t('导入代理配置文件')}}</strong><p>{{t('支持 Webshare TXT、普通节点列表、Clash / Mihomo 和 Base64。')}}</p></div></div>
          <div v-if="importFile" class="private-file-summary" role="status"><Check :size="17"/><div><strong>{{importFile.name}}</strong><span>{{bytes(importFile.size)}} · {{importFile.lines}} {{t('个非空配置行')}}</span></div><button type="button" class="icon" :disabled="busy || importFileReading" :aria-label="t('移除所选文件')" @click="clearPrivateFile"><X :size="16"/></button></div>
          <label class="import-file-button private-file-picker" :class="{ disabled: busy || importFileReading }"><LoaderCircle v-if="importFileReading" :size="16" class="spin"/><Download v-else :size="16"/>{{importFileReading ? t('正在读取文件…') : importFile ? t('重新选择文件') : t('选择文件')}}<input type="file" :disabled="busy || importFileReading" accept=".txt,.yaml,.yml,.json,.conf,text/plain,application/json" :aria-label="t('选择代理配置文件')" @change="readPrivateFile"/></label>
          <p class="field-caption">{{t('最大 4 MiB；文件内容与账号密码不在预览中显示。')}}</p>
        </div>
        <div v-if="importMode !== 'url'" class="private-plain-protocol">
          <label>{{t('无协议前缀的出口类型')}}<select v-model="importForm.plain_protocol" :disabled="busy || importFileReading"><option value="http">{{t('HTTP（默认）')}}</option><option value="socks5">SOCKS5</option></select></label>
          <div><strong>Webshare · IP:{{t('端口')}}:{{t('账号')}}:{{t('密码')}}</strong><p>{{t('仅用于 IP:端口 或 IP:端口:账号:密码 格式；已有协议的节点链接保持原协议。')}}</p><span>{{t('文件和粘贴内容一次性加入独立出口，不创建订阅来源。')}}</span></div>
        </div>
        <div class="import-formats">
          <span
            class="badge neutral"
            v-for="p in [
              'VLESS',
              'VMess',
              'Trojan',
              'Shadowsocks',
              'Hysteria2',
              'HTTP',
              'SOCKS5',
            ]"
            :key="p"
            >{{ p }}</span
          >
        </div>
        <p class="field-caption">{{ t("HTTPS 订阅保存为来源并每日更新；单节点、粘贴内容和文件归入独立出口。一次最多 256 项，重复配置自动跳过。") }}</p>
        <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
        <div v-if="importIssues.length" class="private-import-issues" role="status"><strong>{{t('错误配置')}} · {{importIssues.length}}</strong><p>{{t('未导入任何出口，请修改标记的行后重新选择文件或粘贴。')}}</p><details><summary>{{t('查看错误行')}}</summary><p v-for="issue in importIssues" :key="issue.index">{{t('行 / 项')}} {{issue.index}} · {{t(issue.reason)}}</p></details></div>
        <div class="modal-footer">
          <button type="button" :disabled="busy" @click="modal = ''">{{ t("取消") }}</button
          ><button class="primary" :disabled="busy || importFileReading || (importMode === 'file' && !importFile)">
            <LoaderCircle v-if="busy" class="spin" :size="16" /><Download
              v-else
              :size="16"
            />{{ busy ? t("正在下载并校验…") : t("导入到 IP 池") }}
          </button>
        </div>
      </form>
      <form v-else-if="modal === 'user'" @submit.prevent="saveUser">
        <label
          >{{ t("账号") }}<input
            v-model="userForm.username"
            required
            pattern="[a-zA-Z0-9][a-zA-Z0-9_.\-]{2,31}"
            maxlength="32"
            autocomplete="off" /></label
        ><label
          >{{ editingID ? t("新密码（留空保持不变）") : t("登录密码")
          }}<input
            v-model="userForm.password"
            type="password"
            autocomplete="new-password"
            :required="!editingID"
            maxlength="72"
        /></label><p class="field-help">{{t('密码至少 1 位，无复杂度要求，最多 72 字节。')}}</p>
        <div class="field-row">
          <label
            >{{ t("流量额度 / GB") }}<input
              v-model.number="userForm.quotaGB"
              type="number"
              min="0"
              step="0.01"
              required
            /><small>{{ t("0 为不限") }}</small></label
          ><label
            >{{ t("到期日期") }}<input v-model="userForm.expiresDate" type="date" /><small
              >{{ t("留空为不限") }}</small
            ></label
          >
        </div>
        <div class="check-row">
          <label
            ><input
              v-model="userForm.enabled"
              type="checkbox"
              :disabled="
                state?.users.find((u) => u.id === editingID)?.role === 'owner'
              "
            />{{ t("账号启用") }}</label
          ><label><input v-model="userForm.vless" type="checkbox" />VLESS</label
          ><label><input v-model="userForm.hy2" type="checkbox" />HY2</label>
        </div>
        <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
        <div v-if="editingID" class="advanced-actions">
          <button
            type="button"
            @click="
              confirmUser(
                state!.users.find((u) => u.id === editingID)!,
                'reset-traffic',
              )
            "
          >
            <RefreshCw :size="15" />{{ t("重置流量") }}</button
          ><button
            v-if="
              state?.users.find((u) => u.id === editingID)?.role !== 'owner'
            "
            type="button"
            class="danger-button"
            @click="
              confirmUser(
                state!.users.find((u) => u.id === editingID)!,
                'delete',
              )
            "
          >
            <Trash2 :size="15" />{{ t("删除") }}</button>
        </div>
        <div class="modal-footer">
          <button type="button" :disabled="busy" @click="modal = ''">{{ t("取消") }}</button
          ><button class="primary" :disabled="busy">
            <LoaderCircle v-if="busy" class="spin" :size="16" /><Check
              v-else
              :size="16"
            />{{ t("保存成员") }}</button>
        </div>
      </form>
      <form v-else-if="modal === 'ip'" @submit.prevent="saveIP">
        <label
          >{{ t("备注名称") }}<input
            v-model="ipForm.label"
            maxlength="64"
            :placeholder="t('如：新加坡主出口 / 备用线路')"
        /></label>
        <div class="field-row">
          <label
            >{{ t("出口类型") }}<select
              v-model="ipForm.exit"
              :disabled="ipForm.exit === 'subscription'"
            >
              <option
                v-if="ipForm.exit === 'subscription'"
                value="subscription"
              >{{ t("机场节点 ·") }}{{ ipForm.upstream_type?.toUpperCase() }}
              </option>
              <option value="http">HTTP CONNECT</option>
              <option value="socks5">SOCKS5</option>
            </select></label
          ><label
            >{{ t("资源状态") }}<select
              v-model="ipForm.enabled"
              :disabled="!!ipForm.node_ids?.length"
            >
              <option :value="true">{{ t("启用，可供节点选择") }}</option>
              <option :value="false">{{ t("停用") }}</option>
            </select></label
          >
        </div>
        <template
          v-if="ipForm.exit !== 'direct' && ipForm.exit !== 'subscription'"
          ><div class="field-row host-port">
            <label
              >{{ t("代理服务器") }}<input
                v-model="ipForm.host"
                required
                :placeholder="t('IP 或域名')"
                maxlength="253" /></label
            ><label
              >{{ t("端口") }}<input
                v-model.number="ipForm.port"
                type="number"
                min="1"
                max="65535"
                required
            /></label>
          </div>
          <div class="field-row">
            <label
              >{{ t("认证用户名") }}<input
                v-model="ipForm.username"
                maxlength="128"
                autocomplete="off" /></label
            ><label
              >{{ t("认证密码") }}<input
                v-model="ipForm.password"
                type="password"
                maxlength="256"
                autocomplete="new-password"
                :placeholder="
                  ipForm.has_password ? t('已保存，留空保持不变') : t('无认证可留空')
                "
            /></label></div
        ></template>
        <p v-if="ipForm.exit === 'subscription'" class="field-caption">
          {{ ipForm.host }}:{{ ipForm.port }}{{ t("· 协议参数来自订阅，凭据已加密保存。") }}</p>
        <label
          >{{ t("说明") }}<input
            v-model="ipForm.notes"
            maxlength="240"
            :placeholder="t('供应商、用途或续费备注（可选）')"
        /></label>
        <div v-if="ipForm.node_ids?.length" class="decision-note">
          <CircleHelp :size="16" />
          <div>{{ t("此资源正在被") }}{{ ipForm.node_ids.length }}{{ t("个节点使用。更改出口配置将同步应用到这些节点，并使对应协议的连接重连。") }}<div class="bound-node-list">
              <span
                v-for="n in state?.nodes.filter((n) => n.exit_id === ipForm.id)"
                :key="n.id"
                class="tag"
                :class="n.protocol"
                >{{ n.protocol.toUpperCase() }} · {{ n.name }}</span
              >
            </div>
          </div>
        </div>
        <p v-else class="field-caption">{{ t("保存后自动检测实际公网 IP 和国家。未绑定资源检测失败时仍会入池，并标记为不可用。") }}</p>
        <p v-if="ipForm.exit === 'http'" class="field-caption">{{ t("HTTP CONNECT 出口不支持任意 UDP 流量。") }}</p>
        <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
        <div class="modal-footer">
          <button type="button" :disabled="busy" @click="modal = ''">{{ t("取消") }}</button
          ><button class="primary" :disabled="busy">
            <LoaderCircle v-if="busy" class="spin" :size="16" /><Check
              v-else
              :size="16"
            />{{ ipForm.enabled ? t("检测并保存") : t("保存资源") }}
          </button>
        </div>
      </form>
      <form v-else-if="modal === 'node'" @submit.prevent="saveNode">
        <div class="auto-node-name">
          <span class="field-caption"
            >{{ t("节点名称") }}<span class="badge neutral">{{ t("自动") }}</span></span
          ><strong>{{ displayedNodeName }}</strong>
        </div>
        <div class="check-row">
          <label v-if="!editingDefaultDirect"
            ><input v-model="nodeForm.enabled" type="checkbox" />{{ t("节点启用") }}</label
          ><span v-else class="badge neutral"><ShieldCheck :size="12"/>{{t('默认直连')}}</span><span class="tag" :class="nodeForm.protocol">{{
            nodeForm.protocol.toUpperCase()
          }}</span>
        </div>
        <div v-if="editingDefaultDirect" class="default-direct-notice"><ShieldCheck :size="18"/><div><strong>{{t('保留服务器默认直连入口')}}</strong><p>{{t('此节点固定使用本机出口，保持启用，不能删除或绑定其他出口。')}}</p><button type="button" class="text-button" :disabled="busy" @click="editNode(undefined, nodeForm.protocol)"><Plus :size="14"/>{{t('绑定 IP 池出口请新增节点')}}</button></div></div>
        <div v-if="nodeForm.protocol === 'vless'" class="node-sni-setting">
          <label for="node-reality-sni">{{t('Reality SNI（可选）')}}<input id="node-reality-sni" v-model="nodeForm.reality_sni" type="text" inputmode="url" autocomplete="off" autocapitalize="none" :spellcheck="false" maxlength="63" :disabled="busy" :placeholder="defaultRealitySNI" aria-describedby="node-reality-sni-help"/></label>
          <p id="node-reality-sni-help">{{t('仅填写域名，留空继承默认值')}} <strong>{{defaultRealitySNI}}</strong><br/>{{t('保存时校验公网 TLS 伪装目标；修改后请更新客户端订阅。')}}</p>
        </div>
        <label v-if="!editingDefaultDirect"
          >{{ t("节点出口") }}<select v-model="nodeForm.exit_id" :aria-label="t('节点出口')">
            <option value="">{{ t("本机直连（默认）") }}</option>
            <option
              v-for="p in ipPool"
              :key="p.id"
              :value="p.id"
              :disabled="!p.enabled"
            >
              {{ p.label ? p.label + " · " : "" }}{{ p.name }} ·
              {{ exitName(p.exit) }}{{ !p.enabled ? t("（已停用）") : "" }}
            </option>
          </select></label
        >
        <section v-if="nodeForm.dns" class="node-dns-settings">
          <div class="dns-setting-title"><ShieldCheck :size="17"/><strong>{{t('出口 DNS 防护')}}</strong><span class="badge neutral">IPv4 / IPv6</span></div>
          <label>{{t('DNS 模式')}}<select v-model="nodeForm.dns.mode" :disabled="busy" @change="nodeForm.dns.mode === 'secure' && Object.assign(nodeForm.dns,{doh:nodeForm.dns.doh || 'https://1.1.1.1/dns-query',ipv6:nodeForm.dns.ipv6 || 'block'})"><option value="secure">{{t('经出口加密解析（推荐）')}}</option><option value="system">{{t('常规解析（兼容模式）')}}</option></select></label>
          <template v-if="nodeForm.dns.mode === 'secure'">
            <label>{{t('DNS over HTTPS 地址')}}<input v-model="nodeForm.dns.doh" placeholder="https://1.1.1.1/dns-query" :disabled="busy" autocomplete="off"/></label>
            <div class="dns-presets"><button type="button" :disabled="busy" @click="nodeForm.dns.doh='https://1.1.1.1/dns-query'">Cloudflare</button><button type="button" :disabled="busy" @click="nodeForm.dns.doh='https://8.8.8.8/dns-query'">Google</button></div>
            <label>{{t('IPv6 策略')}}<select v-model="nodeForm.dns.ipv6" :disabled="busy"><option value="block">{{t('阻止 IPv6（推荐）')}}</option><option value="allow">{{t('允许 IPv6（经出口）')}}</option></select></label>
            <p>{{nodeForm.dns.ipv6 === 'block' ? t('阻止 IPv6：不返回 AAAA，拒绝 IPv6 目标') : t('允许 IPv6：A / AAAA 均经所选出口解析')}}</p>
            <p>{{t('域名解析和经过节点的 TCP / UDP 53 查询统一经该出口访问指定 DoH；失败即拒绝，不回退本机 DNS。')}}</p>
            <p>{{t('请输入使用公网 IP 的 HTTPS 地址；需证书有效。IPv6 出口不可用时不会改走 VPS 直连。')}}</p>
          </template>
          <p v-else>{{t('沿用核心与出口的解析方式，不强制 DNS 防护。')}}</p>
          <small>{{t('DNS 检测可能显示递归 DNS 服务商的地址；客户端绕过代理的 DNS 需在客户端开启 TUN / DNS 接管。')}}</small>
        </section>
        <div v-if="selectedIP && !editingDefaultDirect" class="pool-option-preview">
          <div class="user-cell">
            <CountryMark
              :code="selectedIP.country_code"
              :country="selectedIP.country"
            />
            <div>
              <strong>{{ selectedIP.name }}</strong
              ><small>{{ selectedIP.label || t("共享出口资源") }}</small>
            </div>
            <span class="spacer"></span
            ><span :class="['badge', ipBadge(selectedIP)]">{{
              ipStatus(selectedIP)
            }}</span>
          </div>
          <dl class="node-details">
            <div>
              <dt>{{ t("出口方式") }}</dt>
              <dd>{{ exitName(selectedIP.exit) }}</dd>
            </div>
            <div>
              <dt>{{ t("代理地址") }}</dt>
              <dd class="mono">
                {{
                  selectedIP.exit === "direct"
                    ? t("VPS 本机")
                    : selectedIP.host + ":" + selectedIP.port
                }}
              </dd>
            </div>
            <div>
              <dt>{{ t("已绑定节点") }}</dt>
              <dd>{{ selectedIP.node_ids?.length || 0 }}{{ t("个") }}</dd>
            </div>
          </dl>
        </div>
        <div v-else-if="editingDefaultDirect || !nodeForm.exit_id" class="pool-option-preview">
          <div class="user-cell">
            <Globe2 :size="24" />
            <div>
              <strong>{{ t("本机直连") }}</strong
              ><small>{{ t("直接使用此 VPS 的公网 IP，不占用 IP 池资源") }}</small>
            </div>
            <span class="spacer"></span
            ><span class="badge neutral">{{ t("默认出口") }}</span>
          </div>
        </div>
        <div v-if="!editingDefaultDirect" class="pool-select-footer">
          <span>{{ t("需要添加或修改出口") }}</span
          ><button
            type="button"
            class="text-button"
            @click="
              modal = '';
              go('ips');
            "
          >{{ t("前往 IP 管理") }}<ArrowUpRight :size="15" />
          </button>
        </div>
        <p v-if="!editingDefaultDirect || nodeForm.protocol === 'vless'" class="decision-note">
          <CircleHelp :size="16" />{{
            nodeForm.protocol === "hy2"
              ? t("此节点独立选择出口；应用配置会使 HY2 连接短暂重连。")
              : t("应用节点配置会使使用 VLESS 的成员重连。")
          }}{{
            selectedIP?.exit === "http" ? t(" HTTP 出口不支持任意 UDP。") : ""
          }}
        </p>
        <div class="probe-line">
          <button
            type="button"
            :disabled="busy || (!editingDefaultDirect && !!nodeForm.exit_id && !selectedIP?.enabled)"
            @click="probe"
          >
            <Activity :size="16" />{{ t("检测出口") }}</button
          ><span v-if="probeResult" class="probe-success"
            ><Check :size="16" />{{ probeResult }}</span
          >
        </div>
        <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
        <div class="modal-footer">
          <button
            v-if="
              nodeForm.id && !editingDefaultDirect &&
              (state?.nodes.filter((n) => n.protocol === nodeForm.protocol)
                .length || 0) > 1
            "
            type="button"
            class="icon danger-button"
            :title="t('删除节点')"
            @click="deleteNode(nodeForm)"
          >
            <Trash2 :size="17" /></button
          ><span class="spacer"></span
          ><button type="button" :disabled="busy" @click="modal = ''">{{ t("取消") }}</button
          ><button class="primary" :disabled="busy">
            <LoaderCircle v-if="busy" class="spin" :size="16" /><Check
              v-else
              :size="16"
            />{{ t("检测并应用") }}</button>
        </div>
      </form>
      <form v-else @submit.prevent="changePassword">
        <label
          >{{ t("当前密码") }}<input
            v-model="passwordForm.current"
            type="password"
            autocomplete="current-password"
            required /></label
        ><label
          >{{ t("新密码") }}<input
            v-model="passwordForm.password"
            type="password"
            autocomplete="new-password"
            maxlength="72"
            required /></label
        ><label
          >{{ t("确认新密码") }}<input
            v-model="passwordForm.confirm"
            type="password"
            autocomplete="new-password"
            maxlength="72"
            required
        /></label><p class="field-help">{{t('密码至少 1 位，无复杂度要求，最多 72 字节。')}}</p>
        <p v-if="error" class="error" role="alert">{{ t(error) }}</p>
        <div class="modal-footer">
          <button type="button" @click="modal = ''">{{ t("取消") }}</button
          ><button class="primary" :disabled="busy">
            <KeyRound :size="16" />{{ t("修改密码") }}</button>
        </div>
      </form>
    </section>
  </div>
<div v-if="confirmation" class="modal-shade confirm-shade">
    <section ref="confirmDialog" tabindex="-1" class="modal confirmation" role="alertdialog" aria-modal="true" :aria-label="confirmation.title">
      <h2>{{ confirmation.title }}</h2>
      <p>{{ confirmation.detail }}</p>
      <p v-if="error" class="error">{{ t(error) }}</p>
      <div class="modal-footer">
        <button :disabled="busy" @click="confirmation = null">{{ t("取消") }}</button
        ><button class="primary" :disabled="busy" @click="confirmed">
          <LoaderCircle v-if="busy" class="spin" :size="16" />{{ t("确认") }}</button>
      </div>
    </section>
  </div>
<div v-if="notice" class="toast" role="status">
    <Check :size="17" />{{ t(notice) }}
  </div>
</template>

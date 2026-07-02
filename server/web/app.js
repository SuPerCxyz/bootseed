// BootSeed 服务端门户前端(无框架).
'use strict';

const S = {
  info: null, nodes: [], filter: 'all', jobTimer: null, imageEditId: '', clock: null,
  nodePoll: null, activeNode: null, confirmAction: null, activeDetailNodeUUID: '', revealEnterSecret: false,
  deploySubmitting: false, deployStatus: null, deployLoadFailed: false,
};

function token() { return sessionStorage.getItem('portal_token') || ''; }
function authHeaders(extra) {
  const h = extra || {};
  if (token()) h['Authorization'] = 'Bearer ' + token();
  return h;
}
async function api(path, opts) {
  opts = opts || {};
  const r = await fetch(path, opts);
  const t = await r.text();
  let d = {}; try { d = t ? JSON.parse(t) : {}; } catch (e) {}
  if (!r.ok) throw new Error(d.error || ('HTTP ' + r.status));
  return d;
}
function toast(msg, err) {
  const el = document.createElement('div');
  el.className = 'toast-item' + (err ? ' error' : '');
  el.textContent = msg;
  document.getElementById('toast').appendChild(el);
  setTimeout(() => el.remove(), 6000);
}
function humanSize(n) {
  if (!n) return '-';
  const u = ['B', 'KB', 'MB', 'GB', 'TB']; let i = 0, v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return v.toFixed(1) + ' ' + u[i];
}
function esc(v) {
  return String(v || '').replaceAll('&', '&amp;').replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;').replaceAll('"', '&quot;');
}
// 后端时间为 RFC3339(UTC,带 Z).换算成浏览器本地时区显示,避免比本地慢 8 小时.
function fmtTime(s) {
  if (!s || s.startsWith('0001')) return '-';   // 空或 Go 零值时间
  const d = new Date(s);
  if (isNaN(d.getTime())) return s;
  return d.toLocaleString();
}
function tooltipText(lines) {
  return esc((lines || []).filter(Boolean).join('\n'));
}
function badge(cls, text, title) {
  const tip = tooltipText(title);
  const attr = tip ? ` data-tooltip="${tip}" tabindex="0"` : '';
  return `<span class="badge ${cls}"${attr}>${esc(text)}</span>`;
}
function lifecycleBadge(node) {
  if (node.lifecycle === 'deploying') return badge('warn', '部署');
  if (node.lifecycle === 'bootseed_online') return badge('on', '在线');
  if (node.lifecycle === 'completed') return badge('ok', '成功');
  if (node.lifecycle === 'failed') return badge('no', '失败');
  return node.status === 'online' ? badge('on', '在线') : badge('off', '离线');
}
function withTooltip(text, className) {
  const safe = esc(text || '-');
  const attr = text ? ` data-tooltip="${tooltipText([text])}" tabindex="0"` : '';
  return `<span class="${className}"${attr}>${safe}</span>`;
}
function kvRow(key, value) {
  return `<div class="kv-item"><span class="kv-key">${esc(key)}</span><span class="kv-value">${value || '-'}</span></div>`;
}
function detailKV(key, value, mono) {
  const cls = mono ? ' class="mono"' : '';
  return `<dt>${esc(key)}</dt><dd${cls}>${value || '-'}</dd>`;
}
function detailPanel(title, body) {
  return `<section class="detail-panel"><h4>${esc(title)}</h4>${body}</section>`;
}
function stateBadge(state) {
  const text = state || 'idle';
  if (text === 'completed') return badge('ok', '完成');
  if (text === 'failed') return badge('no', '失败');
  if (text === 'cancelled') return badge('off', '已取消');
  if (text === 'idle') return badge('off', '空闲');
  return badge('warn', text);
}
function deployStateBusy(state, active) {
  if (deployStateTerminal(state)) return false;
  return Boolean(active) || ['validating', 'preparing', 'downloading', 'writing', 'syncing', 'verifying'].includes(state || '');
}
function deployStateTerminal(state) {
  return ['completed', 'failed', 'cancelled'].includes(state || '');
}
function deployStatusState(status) {
  if (!status) return 'idle';
  const state = status.task ? status.task.state : '';
  if (deployStateTerminal(state)) return state;
  return state || (status.active ? 'running' : 'idle');
}
function deployResultState(status) {
  const state = deployStatusState(status);
  if (state === 'completed' || state === 'failed' || state === 'cancelled') return state;
  return 'none';
}
function setDeployStartButton(disabled, text) {
  const btn = document.getElementById('node-deploy-start');
  if (!btn) return;
  btn.disabled = Boolean(disabled);
  btn.textContent = text || '确认并开始部署';
}
function updateDeployActionState() {
  const image = document.getElementById('node-image-select');
  const disk = document.getElementById('node-disk-select');
  const confirm = document.getElementById('node-confirm-input');
  if (!image || !disk || !confirm) return;
  if (S.deployLoadFailed) {
    setDeployStartButton(true, '加载失败');
    return;
  }
  const state = deployStatusState(S.deployStatus);
  const busy = deployStateBusy(state, S.deployStatus && S.deployStatus.active);
  const result = deployResultState(S.deployStatus);
  const eraseOK = confirm.value.trim() === 'ERASE';
  const imageOK = Boolean(image.value);
  const diskOK = Boolean(disk.value);
  if (S.deploySubmitting) {
    setDeployStartButton(true, '提交中...');
    return;
  }
  if (busy) {
    setDeployStartButton(true, '部署进行中');
    return;
  }
  if (!imageOK || !diskOK) {
    setDeployStartButton(true, '请选择镜像和磁盘');
    return;
  }
  if (!eraseOK) {
    setDeployStartButton(true, result === 'none' ? '输入ERASE后开始部署' : '输入ERASE后可再次部署');
    return;
  }
  setDeployStartButton(false, result === 'none' ? '确认并开始部署' : '确认并再次部署');
}
function secretValue(secret) {
  if (!secret) return '-';
  const preview = secret.length > 10 ? `${secret.slice(0, 4)}...${secret.slice(-4)}` : secret;
  const shown = S.revealEnterSecret ? secret : preview;
  const label = S.revealEnterSecret ? '隐藏' : '显示';
  return `<span class="secret-value">
    <span class="secret-text">${esc(shown)}</span>
    <span class="secret-actions">
      <button id="enter-secret-toggle" class="btn-sm btn-secondary" type="button">${label}</button>
      <button id="enter-secret-copy" class="btn-sm btn-secondary" type="button">复制</button>
    </span>
  </span>`;
}
function renderServerTime() {
  const el = document.getElementById('server-time');
  if (el) el.textContent = new Date().toLocaleString();
}

async function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const input = document.createElement('textarea');
  input.value = text;
  input.setAttribute('readonly', 'readonly');
  input.style.position = 'fixed';
  input.style.top = '-9999px';
  input.style.left = '-9999px';
  document.body.appendChild(input);
  input.focus();
  input.select();
  const ok = document.execCommand('copy');
  document.body.removeChild(input);
  if (!ok) throw new Error('copy failed');
}

async function loadServerInfo() {
  const c = await api('/api/server-info');
  S.info = c;
  document.getElementById('health').innerHTML = c.healthy
    ? '<span class="status-indicator ok">服务正常</span>'
    : '<span class="status-indicator no">服务异常</span>';
  const g = document.getElementById('overview');
  const rows = [
    ['PXE 服务端 IP', c.pxe_server_ip],
    ['PXE 网卡', c.pxe_interface], ['PXE 子网', c.pxe_subnet],
    ['支持架构', (c.architectures || []).join(', ')], ['Alpine 版本', c.alpine_version],
    ['Agent 版本', c.agent_version], ['iPXE 版本', c.ipxe_ref],
    ['脚本启动密钥', secretValue(c.enter_secret)],
    ['当前时间', '<span id="server-time">-</span>'],
  ];
  g.innerHTML = rows.map(([k, v]) => kvRow(k, v)).join('');
  renderServerTime();
  const toggle = document.getElementById('enter-secret-toggle');
  if (toggle) toggle.addEventListener('click', () => {
    S.revealEnterSecret = !S.revealEnterSecret;
    loadServerInfo();
  });
  const copy = document.getElementById('enter-secret-copy');
  if (copy) copy.addEventListener('click', async () => {
    try {
      await copyText(c.enter_secret || '');
      toast('密钥已复制');
    } catch (e) {
      toast('复制密钥失败', true);
    }
  });

  const ab = document.getElementById('alpine-builds');
  ab.innerHTML = Object.entries(c.alpine_builds || {}).map(([a, b]) => {
    const statusLines = [];
    if (b.note) statusLines.push(`说明: ${b.note}`);
    if ((b.existing_files || []).length) statusLines.push(`存在: ${(b.existing_files || []).join(', ')}`);
    if ((b.missing_files || []).length) statusLines.push(`缺失: ${(b.missing_files || []).join(', ')}`);
    const ready = b.ready
      ? badge('ok', '就绪', statusLines)
      : badge(b.note === '未构建' ? 'off' : 'no', b.note || '未就绪', statusLines);
    const meta = b.kernel_version
      ? `<span><b>内核</b> ${esc(b.kernel_version)}</span><span><b>Alpine</b> ${esc(b.alpine_version)}</span>` +
        `<span><b>驱动</b> ${esc(b.modules)}</span><span><b>固件</b> ${esc(b.firmware)}</span>` +
        `<span><b>构建</b> ${esc(fmtTime(b.build_time))}</span>`
      : `<span>${esc(b.note || '尚未构建该架构')}</span>`;
    return `<div class="build-card" data-arch="${esc(a)}">
      <div class="title"><span>${esc(a)}</span>${ready}</div>
      <div class="meta">${meta}</div>
    </div>`;
  }).join('');
  ab.querySelectorAll('.build-card').forEach((card) => {
    card.addEventListener('click', () => openBuildModal(card.dataset.arch));
  });

  const ip = document.getElementById('ipxe-files');
  ip.innerHTML = (c.ipxe_files || []).map((f) => {
    const tips = [f.exists ? `存在: ${f.path}` : `缺失: ${f.path}`];
    return `<div class="kv-row"><span class="k">${esc(f.path)}</span>${f.exists ? badge('ok', '就绪', tips) : badge('no', '缺失', tips)}</div>`;
  }).join('');

  document.getElementById('guide').textContent =
    `1) 目标机 BIOS/UEFI 设一次性网络(PXE)启动,与本服务同二层;现网 DHCP 负责发 IP.\n` +
    `2) BootSeed ProxyDHCP 返回 boot.ipxe 引导并加载内存系统 Alpine.\n` +
    `3) 节点进入内存系统后会自动向服务端门户注册,随后在服务端门户选择部署镜像和安装目标磁盘.\n` +
    `4) 防火墙放行:UDP 67/69/4011(ProxyDHCP+TFTP) 与服务端 HTTP 端口.\n` +
    `5) 服务端门户:镜像仓库管理、节点列表、部署历史与部署状态.`;
}

async function loadImages() {
  const idx = await api('/api/images');
  const tb = document.querySelector('#image-table tbody');
  const imgs = idx.images || [];
  tb.innerHTML = imgs.map(i => `<tr>
    <td class="mono">${esc(i.id)}</td><td>${esc(i.name || '-')}</td><td>${esc(i.os || '-')}</td>
    <td>${esc(i.version || '-')}</td><td>${esc(i.architecture)} / ${esc((i.firmware || []).join('/'))}</td>
    <td class="td-desc">${withTooltip(i.description || '-', 'truncate-text')}</td>
    <td>${esc(i.format)}</td><td class="num">${humanSize(i.compressed_size)}</td><td class="num">${humanSize(i.raw_size)}</td>
    <td class="actions">
      <span class="action-group">
        <a class="btn btn-sm btn-secondary" href="${i.path}" download>下载</a>
        <button class="btn-sm btn-secondary" data-edit="${i.id}">编辑</button>
        <button class="btn-sm btn-danger" data-del="${i.id}">删除</button>
      </span>
    </td>
  </tr>`).join('') || '<tr><td colspan="10" class="empty">暂无镜像,点击右上「添加镜像」</td></tr>';
  tb.querySelectorAll('button[data-edit]').forEach((b) => {
    b.addEventListener('click', () => openEdit(imgs.find((i) => i.id === b.dataset.edit)));
  });
  tb.querySelectorAll('button[data-del]').forEach(b => b.addEventListener('click', () => delImage(b.dataset.del)));
}

function openConfirmModal(title, message, onConfirm, okText) {
  S.confirmAction = onConfirm || null;
  document.getElementById('confirm-title').textContent = title || '确认操作';
  document.getElementById('confirm-message').textContent = message || '';
  document.getElementById('confirm-ok').textContent = okText || '确认';
  document.getElementById('confirm-modal').classList.add('show');
}

function closeConfirmModal() {
  document.getElementById('confirm-modal').classList.remove('show');
  S.confirmAction = null;
}

function renderNodeDetail(node) {
  if (!node) return '<div class="detail-empty">无节点信息</div>';
  const identity = detailPanel('基础信息', `<dl class="detail-kv">
    ${detailKV('UUID', esc(node.uuid || '-'), true)}
    ${detailKV('主机名', esc(node.hostname || '-'))}
    ${detailKV('来源', esc(node.origin || '-'))}
    ${detailKV('生命周期', lifecycleBadge(node))}
    ${detailKV('结果', node.last_result ? esc(node.last_result) : '-')}
    ${detailKV('最近活动', esc(fmtTime(node.last_seen)))}
  </dl>`);
  const network = detailPanel('网络与启动', `<dl class="detail-kv">
    ${detailKV('节点地址', esc(node.ip || '-'))}
    ${detailKV('管理网卡', esc(node.management_iface || '-'))}
    ${detailKV('网络模式', esc(node.network_mode || '-'))}
    ${detailKV('架构/固件', `${esc(node.arch || '-')} / ${esc(node.boot_mode || '-')}`)}
    ${detailKV('MAC', esc(node.mac || '-'), true)}
    ${detailKV('掩码', esc(node.netmask || '-'))}
    ${detailKV('网关', esc(node.gateway || '-'))}
    ${detailKV('DNS', esc((node.dns || []).join(', ') || '-'))}
    ${detailKV('节点页面', esc(node.agent_url || '-'), true)}
  </dl>`);
  const runtime = detailPanel('运行环境', `<dl class="detail-kv">
    ${detailKV('内核', esc(node.kernel_version || '-'))}
    ${detailKV('Alpine', esc(node.alpine_version || '-'))}
    ${detailKV('Agent 版本', esc(node.agent_version || '-'))}
    ${detailKV('首次上线', esc(fmtTime(node.first_seen)))}
    ${detailKV('最后错误', esc(node.last_error || '-'))}
  </dl>`);
  const deploys = (node.deploys || []).length
    ? `<div class="detail-list">${node.deploys.map((dp) => `<div class="detail-item">
        <div class="detail-item-head">
          <span class="detail-item-title">${esc(dp.image_id || '-')} -> ${esc(dp.target_disk || '-')}</span>
          ${stateBadge(dp.result)}
        </div>
        <div class="detail-item-meta">
          写入 ${humanSize(dp.bytes_written)} | ${fmtTime(dp.started_at)} ~ ${fmtTime(dp.ended_at)}
          ${dp.error ? ` | 错误: ${esc(dp.error)}` : ''}
        </div>
      </div>`).join('')}</div>`
    : '<div class="detail-empty">暂无部署历史</div>';
  const history = detailPanel('部署历史', deploys);
  return `<div class="detail-grid">${identity}${network}${runtime}${history}</div>`;
}

function renderDeploySummary(node) {
  if (!node) return '';
  return '';
}

function renderDeployProgress(st) {
  if (!st) return '<div class="detail-empty">暂无部署状态</div>';
  const p = st.progress || {};
  const state = st.task ? st.task.state : (st.active ? 'running' : 'idle');
  const result = deployResultState(st);
  let percent = 0;
  if (p.total_bytes > 0 && p.written_bytes >= 0) {
    percent = Math.min(100, Math.max(0, (p.written_bytes / p.total_bytes) * 100));
  } else if (state === 'completed') {
    percent = 100;
  } else if (state === 'failed' || state === 'cancelled') {
    percent = 100;
  } else {
    const stagePercent = {
      validating: 5,
      preparing: 10,
      downloading: 20,
      writing: 55,
      syncing: 92,
      verifying: 97,
    };
    percent = stagePercent[state] || stagePercent[p.stage] || 0;
  }
  const barClass = state === 'completed'
    ? 'progress-bar done'
    : (state === 'failed' || state === 'cancelled' || p.error) ? 'progress-bar error' : 'progress-bar';
  let summaryText = '待部署';
  if (S.deploySubmitting) summaryText = '正在提交部署请求';
  else if (deployStateBusy(state, st.active)) summaryText = '部署中';
  else if (result === 'completed') summaryText = '上次部署完成';
  else if (result === 'failed') summaryText = '上次部署异常';
  else if (result === 'cancelled') summaryText = '上次部署已取消';
  const cards = [
    ['下载进度', humanSize(p.downloaded_bytes)],
    ['写入进度', `${humanSize(p.written_bytes)} / ${humanSize(p.total_bytes)}`],
    ['当前写入速度', `${humanSize(p.speed_bps)}/s`],
    ['平均写入速度', `${humanSize(p.average_bps)}/s`],
  ];
  const note = p.error
    ? `<div class="progress-note error">错误: ${esc(p.error)}</div>`
    : '';
  return `<section class="detail-panel">
    <div class="progress-header">
      <span class="progress-title">${esc(summaryText)}</span>
      <span class="progress-percent">${percent.toFixed(1)}%</span>
    </div>
    <div class="${barClass}"><div class="progress-bar-fill" style="width:${percent.toFixed(1)}%"></div></div>
    <div class="progress-card">
      ${cards.map(([label, value]) => `<div class="progress-metric"><span class="label">${esc(label)}</span><span class="value">${esc(value || '-')}</span></div>`).join('')}
    </div>
    ${note}
  </section>`;
}

function openNodeDetail(uuid) {
  const node = S.nodes.find((item) => item.uuid === uuid);
  if (!node) return;
  S.activeDetailNodeUUID = uuid;
  document.getElementById('node-detail-title').textContent = `节点详情: ${node.hostname || node.ip || node.uuid}`;
  document.getElementById('node-detail-body').innerHTML = renderNodeDetail(node);
  document.getElementById('node-detail-modal').classList.add('show');
}

function closeNodeDetail() {
  document.getElementById('node-detail-modal').classList.remove('show');
  S.activeDetailNodeUUID = '';
}

async function runConfirmAction() {
  if (!S.confirmAction) {
    closeConfirmModal();
    return;
  }
  const action = S.confirmAction;
  closeConfirmModal();
  await action();
}

function delImage(id) {
  openConfirmModal(
    '删除镜像',
    `确认删除镜像 ${id} 及其对应文件?`,
    async () => {
      try {
        await api('/api/images/' + encodeURIComponent(id), { method: 'DELETE', headers: authHeaders() });
        toast('已删除 ' + id);
        loadImages();
      } catch (e) {
        toast('删除失败: ' + e.message, true);
      }
    },
    '确认删除'
  );
}

async function loadNodes() {
  const d = await api('/api/nodes');
  S.nodes = d.nodes || [];
  document.getElementById('nodes-summary').textContent = `共 ${d.total} 台,在线 ${d.online} 台`;
  if (S.activeDetailNodeUUID) {
    const node = S.nodes.find((item) => item.uuid === S.activeDetailNodeUUID);
    if (node) {
      document.getElementById('node-detail-title').textContent = `节点详情: ${node.hostname || node.ip || node.uuid}`;
      document.getElementById('node-detail-body').innerHTML = renderNodeDetail(node);
    }
  }
  renderNodes();
}

function delNode(uuid) {
  const node = S.nodes.find((item) => item.uuid === uuid);
  const name = (node && (node.hostname || node.ip || node.uuid)) || uuid;
  openConfirmModal(
    '删除节点',
    `确认删除节点 ${name} 的记录和部署历史?`,
    async () => {
      try {
        await api('/api/nodes/' + encodeURIComponent(uuid), {
          method: 'DELETE',
        });
        toast('节点记录已删除');
        if (S.activeNode && S.activeNode.uuid === uuid) {
          closeNodeDeploy();
        }
        loadNodes();
      } catch (e) {
        toast('删除节点失败: ' + e.message, true);
      }
    },
    '确认删除'
  );
}

function renderNodes() {
  const tb = document.querySelector('#node-table tbody');
  const f = S.filter;
  const list = S.nodes.filter(n => {
    if (f === 'online') return n.status === 'online';
    if (f === 'deployed') return n.deployed_ever;
    if (f === 'undeployed') return !n.deployed_ever;
    if (f === 'failed') return n.last_result === 'failed';
    return true;
  });
  if (!list.length) { tb.innerHTML = '<tr><td colspan="12">无匹配节点</td></tr>'; return; }
  tb.innerHTML = list.map((n, i) => {
    const st = lifecycleBadge(n);
    const lr = n.last_result ? (n.last_result === 'completed'
      ? '<span class="badge ok">成功</span>'
      : `<span class="badge ${n.last_result === 'failed' ? 'no' : 'warn'}">${n.last_result}</span>`) : '-';
    const net = n.network_mode || '-';
    const actionItems = [];
    if (n.status === 'online') {
      actionItems.push(`<button class="btn-sm btn-primary" data-node-deploy="${n.uuid}">部署镜像</button>`);
      if (n.agent_url) {
        actionItems.push(`<a class="btn btn-sm btn-secondary" href="${esc(n.agent_url)}" target="_blank" rel="noreferrer">节点页面</a>`);
      }
    }
    actionItems.push(`<button class="btn-sm btn-danger" data-node-del="${n.uuid}">删除</button>`);
    const actions = `<span class="action-group">${actionItems.join('')}</span>`;
    const main = `<tr class="clickable" data-idx="${i}">
      <td>${st}</td><td>${esc(n.hostname || '-')}</td><td>${n.ip || '-'}</td><td>${esc(n.origin || '-')}</td><td>${esc(net)}</td><td>${esc(n.arch || '-')} / ${esc(n.boot_mode || '-')}</td>
      <td class="mono">${(n.uuid || '').slice(0, 8)}</td><td class="mono">${n.mac || '-'}</td>
      <td>${n.deployed_ever ? '是' : '否'}</td><td>${lr}</td><td>${fmtTime(n.last_seen)}</td><td class="actions">${actions}</td></tr>`;
    return main;
  }).join('');
  tb.querySelectorAll('tr.clickable').forEach(tr => tr.addEventListener('click', () => {
    const node = list[Number(tr.dataset.idx)];
    if (node) openNodeDetail(node.uuid);
  }));
  tb.querySelectorAll('button[data-node-deploy]').forEach((btn) => {
    btn.addEventListener('click', (ev) => {
      ev.stopPropagation();
      openNodeDeploy(btn.dataset.nodeDeploy);
    });
  });
  tb.querySelectorAll('button[data-node-del]').forEach((btn) => {
    btn.addEventListener('click', (ev) => {
      ev.stopPropagation();
      delNode(btn.dataset.nodeDel);
    });
  });
}

// ---- 添加镜像 ----
function setField(id, value) { document.getElementById(id).value = value || ''; }
function resetImageForm() {
  S.imageEditId = '';
  document.getElementById('image-modal-title').textContent = '添加镜像';
  document.getElementById('add-submit').textContent = '提交';
  document.getElementById('image-source-section').style.display = '';
  document.getElementById('add-mode').disabled = false;
  document.getElementById('add-id').disabled = false;
  ['add-id', 'add-name', 'add-os', 'add-version', 'add-source', 'add-desc'].forEach((id) => setField(id, ''));
  setField('add-arch', 'x86_64');
  setField('add-fw', 'bios');
  document.getElementById('add-file').value = '';
  document.getElementById('add-progress').textContent = '';
  onModeChange();
}
function openAdd() {
  resetImageForm();
  document.getElementById('add-modal').classList.add('show');
}
function openEdit(image) {
  if (!image) return;
  resetImageForm();
  S.imageEditId = image.id;
  document.getElementById('image-modal-title').textContent = '编辑镜像元数据';
  document.getElementById('add-submit').textContent = '保存';
  document.getElementById('image-source-section').style.display = 'none';
  document.getElementById('add-id').disabled = true;
  setField('add-id', image.id);
  setField('add-name', image.name);
  setField('add-os', image.os);
  setField('add-version', image.version);
  setField('add-arch', image.architecture);
  setField('add-fw', (image.firmware || []).join(','));
  setField('add-desc', image.description);
  document.getElementById('add-modal').classList.add('show');
}
function closeAdd() {
  document.getElementById('add-modal').classList.remove('show');
  if (S.jobTimer) clearInterval(S.jobTimer);
}
function openBuildModal(arch) {
  const build = (S.info && S.info.alpine_builds) ? S.info.alpine_builds[arch] : null;
  if (!build) return;
  document.getElementById('build-modal-title').textContent = `${arch} 内存系统详情`;
  document.getElementById('build-modules').textContent =
    (build.included_modules || []).length ? build.included_modules.join('\n') : '无驱动清单';
  document.getElementById('build-firmware').textContent =
    (build.included_firmware || []).length ? build.included_firmware.join('\n') : '无固件清单';
  document.getElementById('build-tools').textContent =
    (build.included_tools || []).length ? build.included_tools.join('\n') : '无工具清单';
  document.getElementById('build-modal').classList.add('show');
}
function closeBuildModal() {
  document.getElementById('build-modal').classList.remove('show');
}
function openTokenModal() {
  document.getElementById('token-input').value = token();
  document.getElementById('token-modal').classList.add('show');
}
function closeTokenModal() {
  document.getElementById('token-modal').classList.remove('show');
}
async function openNodeDeploy(uuid) {
  const node = S.nodes.find((item) => item.uuid === uuid);
  if (!node) return;
  S.activeNode = node;
  S.deploySubmitting = false;
  S.deployStatus = null;
  S.deployLoadFailed = false;
  document.getElementById('node-deploy-title').textContent = `部署确认: ${node.uuid}`;
  const summary = document.getElementById('node-deploy-summary');
  summary.innerHTML = renderDeploySummary(node);
  summary.style.display = summary.innerHTML ? '' : 'none';
  document.getElementById('node-deploy-progress').innerHTML = '<div class="detail-empty">正在加载部署镜像、目标磁盘和部署状态...</div>';
  document.getElementById('node-confirm-input').value = '';
  setDeployStartButton(true, '加载中');
  const cancelBtn = document.getElementById('node-deploy-cancel');
  if (cancelBtn) cancelBtn.disabled = true;
  document.getElementById('node-deploy-modal').classList.add('show');
  if (S.nodePoll) clearInterval(S.nodePoll);
  try {
    const [images, disks] = await Promise.all([
      api(`/api/nodes/${encodeURIComponent(uuid)}/agent-images`),
      api(`/api/nodes/${encodeURIComponent(uuid)}/agent-disks`),
    ]);
    const imageSelect = document.getElementById('node-image-select');
    const diskSelect = document.getElementById('node-disk-select');
    imageSelect.innerHTML = (images.images || []).filter((img) => img.compatible).map((img) =>
      `<option value="${esc(img.id)}">${esc(img.name)} | ${esc(img.architecture)} | ${humanSize(img.raw_size)}</option>`).join('') || '<option value="">无可用镜像</option>';
    diskSelect.innerHTML = (disks.disks || []).filter((disk) => disk.allowed).map((disk) => {
      const target = disk.stable_path || disk.path;
      return `<option value="${esc(target)}">${esc(target)} | ${humanSize(disk.size)} | ${esc(disk.model || '')}</option>`;
    }).join('') || '<option value="">无可用磁盘</option>';
    updateDeployActionState();
    await refreshNodeDeployStatus();
    S.nodePoll = setInterval(refreshNodeDeployStatus, 1500);
  } catch (e) {
    S.deployLoadFailed = true;
    document.getElementById('node-deploy-progress').innerHTML = `<div class="progress-note error">加载失败: ${esc(e.message)}</div>`;
    updateDeployActionState();
  }
}
function closeNodeDeploy() {
  document.getElementById('node-deploy-modal').classList.remove('show');
  if (S.nodePoll) clearInterval(S.nodePoll);
  S.nodePoll = null;
  S.activeNode = null;
  S.deploySubmitting = false;
  S.deployStatus = null;
  S.deployLoadFailed = false;
  setDeployStartButton(true, '确认并开始部署');
  const cancelBtn = document.getElementById('node-deploy-cancel');
  if (cancelBtn) cancelBtn.disabled = true;
}
async function refreshNodeDeployStatus() {
  if (!S.activeNode) return;
  try {
    const st = await api(`/api/nodes/${encodeURIComponent(S.activeNode.uuid)}/deploy-status`);
    S.deployLoadFailed = false;
    S.deployStatus = st;
    document.getElementById('node-deploy-progress').innerHTML = renderDeployProgress(st);
    const state = st.task ? st.task.state : (st.active ? 'running' : 'idle');
    if (deployStateTerminal(state)) S.deploySubmitting = false;
    const cancelBtn = document.getElementById('node-deploy-cancel');
    if (cancelBtn) cancelBtn.disabled = !deployStateBusy(state, st.active);
    updateDeployActionState();
  } catch (e) {
    document.getElementById('node-deploy-progress').innerHTML = `<div class="progress-note error">部署状态读取失败: ${esc(e.message)}</div>`;
    const cancelBtn = document.getElementById('node-deploy-cancel');
    if (cancelBtn) cancelBtn.disabled = true;
    updateDeployActionState();
  }
}
async function startNodeDeploy() {
  if (!S.activeNode) return;
  if (S.deploySubmitting) return;
  if (document.getElementById('node-confirm-input').value.trim() !== 'ERASE') {
    toast('确认词必须为 ERASE', true);
    return;
  }
  const body = {
    image_id: document.getElementById('node-image-select').value,
    target_disk: document.getElementById('node-disk-select').value,
    confirmation: 'ERASE',
    verify_raw: document.getElementById('node-verify-raw').checked,
    auto_reboot: document.getElementById('node-auto-reboot').checked,
  };
  try {
    S.deploySubmitting = true;
    setDeployStartButton(true, '提交中...');
    const cancelBtn = document.getElementById('node-deploy-cancel');
    if (cancelBtn) cancelBtn.disabled = true;
    await api(`/api/nodes/${encodeURIComponent(S.activeNode.uuid)}/deploy`, {
      method: 'POST',
      headers: authHeaders({ 'Content-Type': 'application/json' }),
      body: JSON.stringify(body),
    });
    toast('部署任务已提交');
    await refreshNodeDeployStatus();
    loadNodes();
  } catch (e) {
    S.deploySubmitting = false;
    updateDeployActionState();
    toast('提交部署失败: ' + e.message, true);
  }
}
async function cancelNodeDeploy() {
  if (!S.activeNode) return;
  try {
    await api(`/api/nodes/${encodeURIComponent(S.activeNode.uuid)}/deploy-cancel`, {
      method: 'POST', headers: authHeaders(),
    });
    toast('已请求取消当前部署');
    await refreshNodeDeployStatus();
  } catch (e) {
    toast('取消部署失败: ' + e.message, true);
  }
}
function onModeChange() {
  if (S.imageEditId) return;
  const m = document.getElementById('add-mode').value;
  document.getElementById('src-url-l').style.display = (m === 'upload') ? 'none' : '';
  document.getElementById('src-file-l').style.display = (m === 'upload') ? '' : 'none';
  document.getElementById('src-url-l').querySelector('input').placeholder =
    (m === 'path') ? '/data/http/images/上传区/xxx.qcow2' : 'https://.../xxx.qcow2';
}
async function submitAdd() {
  const meta = {
    id: document.getElementById('add-id').value.trim(),
    name: document.getElementById('add-name').value.trim(),
    os: document.getElementById('add-os').value.trim(),
    version: document.getElementById('add-version').value.trim(),
    architecture: document.getElementById('add-arch').value,
    firmware: document.getElementById('add-fw').value.split(','),
    description: document.getElementById('add-desc').value.trim(),
  };
  try {
    if (S.imageEditId) {
      await api('/api/images/' + encodeURIComponent(S.imageEditId), {
        method: 'PUT', headers: authHeaders({ 'Content-Type': 'application/json' }),
        body: JSON.stringify(meta),
      });
      toast('镜像元数据已更新');
      closeAdd();
      loadImages();
      return;
    }
    const mode = document.getElementById('add-mode').value;
    let job;
    if (mode === 'upload') {
      const f = document.getElementById('add-file').files[0];
      if (!f) { toast('请选择文件', true); return; }
      const fd = new FormData();
      fd.append('file', f);
      Object.entries(meta).forEach(([k, v]) => fd.append(k, Array.isArray(v) ? v.join(',') : v));
      job = await api('/api/images/upload', { method: 'POST', headers: authHeaders(), body: fd });
    } else {
      job = await api('/api/images', {
        method: 'POST', headers: authHeaders({ 'Content-Type': 'application/json' }),
        body: JSON.stringify(Object.assign({ mode, source: document.getElementById('add-source').value.trim() }, meta)),
      });
    }
    pollJob(job.job_id);
  } catch (e) { toast('提交失败: ' + e.message, true); }
}
function pollJob(jobId) {
  if (S.jobTimer) clearInterval(S.jobTimer);
  S.jobTimer = setInterval(async () => {
    try {
      const j = await api('/api/images/jobs/' + jobId);
      document.getElementById('add-progress').textContent =
        `阶段: ${j.stage}  ${j.percent ? j.percent.toFixed(1) + '%' : ''}  ${j.message || ''}${j.error ? '\n错误: ' + j.error : ''}`;
      if (j.done) {
        clearInterval(S.jobTimer);
        if (j.error) toast('添加失败: ' + j.error, true);
        else { toast('镜像添加完成'); loadImages(); setTimeout(closeAdd, 1200); }
      }
    } catch (e) {}
  }, 1000);
}

function setToken() {
  const t = document.getElementById('token-input').value.trim();
  sessionStorage.setItem('portal_token', t);
  closeTokenModal();
  toast('口令已保存到本会话');
}

function bind() {
  const tokenBtn = document.getElementById('token-btn');
  if (tokenBtn) tokenBtn.addEventListener('click', openTokenModal);
  document.getElementById('add-image-btn').addEventListener('click', openAdd);
  document.getElementById('add-cancel').addEventListener('click', closeAdd);
  document.getElementById('add-x').addEventListener('click', closeAdd);
  document.getElementById('build-close').addEventListener('click', closeBuildModal);
  document.getElementById('build-x').addEventListener('click', closeBuildModal);
  const tokenCancel = document.getElementById('token-cancel');
  const tokenX = document.getElementById('token-x');
  const tokenSave = document.getElementById('token-save');
  if (tokenCancel) tokenCancel.addEventListener('click', closeTokenModal);
  if (tokenX) tokenX.addEventListener('click', closeTokenModal);
  if (tokenSave) tokenSave.addEventListener('click', setToken);
  document.getElementById('confirm-cancel').addEventListener('click', closeConfirmModal);
  document.getElementById('confirm-x').addEventListener('click', closeConfirmModal);
  document.getElementById('confirm-ok').addEventListener('click', runConfirmAction);
  document.getElementById('node-detail-close').addEventListener('click', closeNodeDetail);
  document.getElementById('node-detail-x').addEventListener('click', closeNodeDetail);
  document.getElementById('node-deploy-close').addEventListener('click', closeNodeDeploy);
  document.getElementById('node-deploy-x').addEventListener('click', closeNodeDeploy);
  document.getElementById('node-deploy-start').addEventListener('click', startNodeDeploy);
  document.getElementById('node-deploy-cancel').addEventListener('click', cancelNodeDeploy);
  document.getElementById('node-confirm-input').addEventListener('input', updateDeployActionState);
  document.getElementById('node-image-select').addEventListener('change', updateDeployActionState);
  document.getElementById('node-disk-select').addEventListener('change', updateDeployActionState);
  document.getElementById('add-submit').addEventListener('click', submitAdd);
  document.getElementById('add-mode').addEventListener('change', onModeChange);
  document.getElementById('reload-nodes').addEventListener('click', loadNodes);
  document.getElementById('node-filter').addEventListener('change', (e) => { S.filter = e.target.value; renderNodes(); });
  ['add-modal', 'build-modal', 'node-deploy-modal', 'confirm-modal', 'node-detail-modal'].forEach((id) => {
    const modal = document.getElementById(id);
    if (!modal) return;
    modal.addEventListener('click', (ev) => {
      if (ev.target !== modal) return;
      if (id === 'add-modal') closeAdd();
      else if (id === 'build-modal') closeBuildModal();
      else if (id === 'node-deploy-modal') closeNodeDeploy();
      else if (id === 'confirm-modal') closeConfirmModal();
      else if (id === 'node-detail-modal') closeNodeDetail();
    });
  });
}

function startClock() {
  if (S.clock) return;
  renderServerTime();
  S.clock = setInterval(renderServerTime, 1000);
}

async function refreshAll() {
  try { await Promise.all([loadServerInfo(), loadImages(), loadNodes()]); }
  catch (e) { toast('刷新失败: ' + e.message, true); }
}

async function init() {
  bind(); startClock(); onModeChange();
  await refreshAll();
  setInterval(() => { loadServerInfo().catch(() => {}); loadNodes().catch(() => {}); }, 10000);
}
init();

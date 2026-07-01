// BootSeed 服务端门户前端(无框架).
'use strict';

const S = { info: null, nodes: [], filter: 'all', jobTimer: null, imageEditId: '', clock: null, nodePoll: null, activeNode: null };

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
  if (node.lifecycle === 'deploying') return badge('warn', '部署中');
  if (node.lifecycle === 'bootseed_online') return badge('on', 'BootSeed 在线');
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
function renderServerTime() {
  const el = document.getElementById('server-time');
  if (el) el.textContent = new Date().toLocaleString();
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
    ['当前时间', '<span id="server-time">-</span>'],
  ];
  g.innerHTML = rows.map(([k, v]) => kvRow(k, v)).join('');
  renderServerTime();

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
    <td>${esc(i.version || '-')}</td><td>${esc(i.architecture)}</td><td>${esc((i.firmware || []).join('/'))}</td>
    <td class="td-desc">${withTooltip(i.description || '-', 'truncate-text')}</td>
    <td>${esc(i.format)}</td><td class="num">${humanSize(i.compressed_size)}</td><td class="num">${humanSize(i.raw_size)}</td>
    <td class="actions">
      <span class="action-group">
        <a class="btn btn-sm btn-secondary" href="${i.path}" download>下载</a>
        <button class="btn-sm btn-secondary" data-edit="${i.id}">编辑</button>
        <button class="btn-sm btn-danger" data-del="${i.id}">删除</button>
      </span>
    </td>
  </tr>`).join('') || '<tr><td colspan="11" class="empty">暂无镜像,点击右上「添加镜像」</td></tr>';
  tb.querySelectorAll('button[data-edit]').forEach((b) => {
    b.addEventListener('click', () => openEdit(imgs.find((i) => i.id === b.dataset.edit)));
  });
  tb.querySelectorAll('button[data-del]').forEach(b => b.addEventListener('click', () => delImage(b.dataset.del)));
}

async function delImage(id) {
  if (!confirm('确认删除镜像 ' + id + '(含文件)?')) return;
  try {
    await api('/api/images/' + encodeURIComponent(id), { method: 'DELETE', headers: authHeaders() });
    toast('已删除 ' + id); loadImages();
  } catch (e) { toast('删除失败: ' + e.message, true); }
}

async function loadNodes() {
  const d = await api('/api/nodes');
  S.nodes = d.nodes || [];
  document.getElementById('nodes-summary').textContent = `共 ${d.total} 台,在线 ${d.online} 台`;
  renderNodes();
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
  if (!list.length) { tb.innerHTML = '<tr><td colspan="13">无匹配节点</td></tr>'; return; }
  tb.innerHTML = list.map((n, i) => {
    const st = lifecycleBadge(n);
    const lr = n.last_result ? (n.last_result === 'completed'
      ? '<span class="badge ok">成功</span>'
      : `<span class="badge ${n.last_result === 'failed' ? 'no' : 'warn'}">${n.last_result}</span>`) : '-';
    const net = `${n.network_mode || '-'}${n.network_status ? ` / ${n.network_status}` : ''}`;
    const actions = n.status === 'online'
      ? `<span class="action-group">
          <button class="btn-sm btn-primary" data-node-deploy="${n.uuid}">部署镜像</button>
          ${n.agent_url ? `<a class="btn btn-sm btn-secondary" href="${esc(n.agent_url)}" target="_blank" rel="noreferrer">节点页面</a>` : ''}
        </span>`
      : '-';
    const main = `<tr class="clickable" data-idx="${i}">
      <td>${st}</td><td>${esc(n.hostname || '-')}</td><td>${n.ip || '-'}</td><td>${esc(n.origin || '-')}</td><td>${esc(net)}</td><td>${n.arch || '-'}</td><td>${n.boot_mode || '-'}</td>
      <td class="mono">${(n.uuid || '').slice(0, 8)}</td><td class="mono">${n.mac || '-'}</td>
      <td>${n.deployed_ever ? '是' : '否'}</td><td>${lr}</td><td>${fmtTime(n.last_seen)}</td><td class="actions">${actions}</td></tr>`;
    const deploys = (n.deploys || []).map(dp =>
      `镜像 ${dp.image_id} -> ${dp.target_disk} ${dp.result} ${humanSize(dp.bytes_written)} ${fmtTime(dp.started_at)}~${fmtTime(dp.ended_at)}${dp.error ? ' 错误:' + dp.error : ''}`
    ).join('\n') || '(未部署过)';
    const detail = `<tr class="row-detail" id="rd-${i}" style="display:none"><td colspan="13"><div class="mono">UUID: ${n.uuid}
主机名: ${n.hostname || '-'}  来源: ${n.origin || '-'}  生命周期: ${n.lifecycle || '-'}
管理网卡: ${n.management_iface || '-'}  IP: ${n.ip || '-'}  掩码: ${n.netmask || '-'}  网关: ${n.gateway || '-'}
DNS: ${(n.dns || []).join(', ') || '-'}
节点页面: ${n.agent_url || '-'}
内核: ${n.kernel_version || '-'}  Alpine: ${n.alpine_version || '-'}  Agent 版本: ${n.agent_version || '-'}
首次: ${fmtTime(n.first_seen)}
错误: ${n.last_error || '-'}
--- 部署历史 ---
${deploys}</div></td></tr>`;
    return main + detail;
  }).join('');
  tb.querySelectorAll('tr.clickable').forEach(tr => tr.addEventListener('click', () => {
    const rd = document.getElementById('rd-' + tr.dataset.idx);
    if (rd) rd.style.display = rd.style.display === 'none' ? '' : 'none';
  }));
  tb.querySelectorAll('button[data-node-deploy]').forEach((btn) => {
    btn.addEventListener('click', (ev) => {
      ev.stopPropagation();
      openNodeDeploy(btn.dataset.nodeDeploy);
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
  document.getElementById('node-deploy-title').textContent = `部署确认: ${node.hostname || node.uuid}`;
  document.getElementById('node-deploy-summary').textContent =
    `节点主机名: ${node.hostname || '-'}  节点地址: ${node.ip || '-'}\n来源: ${node.origin || '-'}  网络: ${node.network_mode || '-'} / ${node.network_status || '-'}\n节点页面: ${node.agent_url || '-'}`;
  document.getElementById('node-deploy-progress').textContent = '正在加载部署镜像、目标磁盘和部署状态...';
  document.getElementById('node-confirm-input').value = '';
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
    await refreshNodeDeployStatus();
    S.nodePoll = setInterval(refreshNodeDeployStatus, 1500);
  } catch (e) {
    document.getElementById('node-deploy-progress').textContent = `加载失败: ${e.message}`;
  }
}
function closeNodeDeploy() {
  document.getElementById('node-deploy-modal').classList.remove('show');
  if (S.nodePoll) clearInterval(S.nodePoll);
  S.nodePoll = null;
  S.activeNode = null;
}
async function refreshNodeDeployStatus() {
  if (!S.activeNode) return;
  try {
    const st = await api(`/api/nodes/${encodeURIComponent(S.activeNode.uuid)}/deploy-status`);
    const p = st.progress || {};
    document.getElementById('node-deploy-progress').textContent =
      `部署状态: ${st.task ? st.task.state : (st.active ? 'running' : 'idle')}\n` +
      `当前阶段: ${p.stage || '-'} ${p.message || ''}\n` +
      `下载: ${humanSize(p.downloaded_bytes)}  写入: ${humanSize(p.written_bytes)} / ${humanSize(p.total_bytes)}\n` +
      `当前写入速度: ${humanSize(p.speed_bps)}/s  平均写入速度: ${humanSize(p.average_bps)}/s\n` +
      `${p.error ? '错误: ' + p.error : ''}`;
  } catch (e) {
    document.getElementById('node-deploy-progress').textContent = `部署状态读取失败: ${e.message}`;
  }
}
async function startNodeDeploy() {
  if (!S.activeNode) return;
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
    await api(`/api/nodes/${encodeURIComponent(S.activeNode.uuid)}/deploy`, {
      method: 'POST',
      headers: authHeaders({ 'Content-Type': 'application/json' }),
      body: JSON.stringify(body),
    });
    toast('部署任务已提交');
    await refreshNodeDeployStatus();
    loadNodes();
  } catch (e) {
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
  document.getElementById('token-btn').addEventListener('click', openTokenModal);
  document.getElementById('add-image-btn').addEventListener('click', openAdd);
  document.getElementById('add-cancel').addEventListener('click', closeAdd);
  document.getElementById('add-x').addEventListener('click', closeAdd);
  document.getElementById('build-close').addEventListener('click', closeBuildModal);
  document.getElementById('build-x').addEventListener('click', closeBuildModal);
  document.getElementById('token-cancel').addEventListener('click', closeTokenModal);
  document.getElementById('token-x').addEventListener('click', closeTokenModal);
  document.getElementById('token-save').addEventListener('click', setToken);
  document.getElementById('node-deploy-close').addEventListener('click', closeNodeDeploy);
  document.getElementById('node-deploy-x').addEventListener('click', closeNodeDeploy);
  document.getElementById('node-deploy-start').addEventListener('click', startNodeDeploy);
  document.getElementById('node-deploy-cancel').addEventListener('click', cancelNodeDeploy);
  document.getElementById('add-submit').addEventListener('click', submitAdd);
  document.getElementById('add-mode').addEventListener('change', onModeChange);
  document.getElementById('reload-nodes').addEventListener('click', loadNodes);
  document.getElementById('node-filter').addEventListener('change', (e) => { S.filter = e.target.value; renderNodes(); });
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

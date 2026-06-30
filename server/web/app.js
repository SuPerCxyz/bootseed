// BootSeed 服务端门户前端（无框架）。
'use strict';

const S = { info: null, nodes: [], filter: 'all', jobTimer: null };

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
// 后端时间为 RFC3339(UTC，带 Z)。换算成浏览器本地时区显示，避免比本地慢 8 小时。
function fmtTime(s) {
  if (!s || s.startsWith('0001')) return '-';   // 空或 Go 零值时间
  const d = new Date(s);
  if (isNaN(d.getTime())) return s;
  return d.toLocaleString();
}

async function loadServerInfo() {
  const c = await api('/api/server-info');
  S.info = c;
  document.getElementById('health').textContent = c.healthy ? '● 服务正常' : '○ 异常';
  const g = document.getElementById('overview');
  const rows = [
    ['PXE 服务端 IP', c.pxe_server_ip], ['HTTP 端口', c.http_port],
    ['PXE 网卡', c.pxe_interface], ['PXE 子网', c.pxe_subnet],
    ['支持架构', (c.architectures || []).join(', ')], ['Alpine 版本', c.alpine_version],
    ['Agent 版本', c.agent_version], ['iPXE 版本', c.ipxe_ref],
  ];
  g.innerHTML = rows.map(([k, v]) => `<div><span>${k}：</span>${v || '-'}</div>`).join('');

  const ab = document.getElementById('alpine-builds');
  ab.innerHTML = Object.entries(c.alpine_builds || {}).map(([a, b]) => {
    const ready = b.ready
      ? '<span class="badge ok">就绪</span>'
      : `<span class="badge ${b.note === '未构建' ? 'off' : 'no'}">${b.note || '未就绪'}</span>`;
    const meta = b.kernel_version
      ? `<span><b>内核</b> ${b.kernel_version}</span><span><b>Alpine</b> ${b.alpine_version}</span>` +
        `<span><b>驱动</b> ${b.modules}</span><span><b>固件</b> ${b.firmware}</span>` +
        `<span><b>构建</b> ${fmtTime(b.build_time)}</span>`
      : '<span>尚未构建该架构</span>';
    return `<div class="build-card">
      <div class="title"><span>${a}</span>${ready}</div>
      <div class="meta">${meta}</div>
    </div>`;
  }).join('');

  const ip = document.getElementById('ipxe-files');
  ip.innerHTML = Object.entries(c.ipxe_files || {}).map(([f, ok]) =>
    `<div class="kv-row"><span class="k">${f}</span>${ok ? '<span class="badge ok">就绪</span>' : '<span class="badge no">缺失</span>'}</div>`).join('');

  const base = `http://${c.pxe_server_ip}:${c.http_port}`;
  document.getElementById('guide').textContent =
    `1) 目标机 BIOS/UEFI 设一次性网络(PXE)启动，与本服务同二层；现网 DHCP 负责发 IP。\n` +
    `2) BootSeed ProxyDHCP 返回引导：${base}/boot/boot.ipxe → 加载内存系统 Alpine。\n` +
    `3) 节点进入内存系统后，控制台/VNC 显示其管理地址 http://<节点IP>:${c.http_port}，在该页选镜像/磁盘部署。\n` +
    `4) 防火墙放行：UDP 67/69/4011(ProxyDHCP+TFTP)、TCP ${c.http_port}(HTTP)。\n` +
    `5) 本门户：镜像增删、查看所有连接过的节点与部署结果。`;
}

async function loadImages() {
  const idx = await api('/api/images');
  const tb = document.querySelector('#image-table tbody');
  const imgs = idx.images || [];
  tb.innerHTML = imgs.map(i => `<tr>
    <td class="mono">${i.id}</td><td>${i.name || '-'}</td><td>${i.os || '-'}</td>
    <td>${i.version || '-'}</td><td>${i.architecture}</td><td>${(i.firmware || []).join('/')}</td>
    <td>${i.format}</td><td class="num">${humanSize(i.compressed_size)}</td><td class="num">${humanSize(i.raw_size)}</td>
    <td class="actions">
      <a class="btn btn-sm btn-secondary" href="${i.path}" download>下载</a>
      <button class="btn-sm btn-danger" data-del="${i.id}">删除</button>
    </td>
  </tr>`).join('') || '<tr><td colspan="10" class="empty">暂无镜像，点击右上「添加镜像」</td></tr>';
  tb.querySelectorAll('button[data-del]').forEach(b => b.addEventListener('click', () => delImage(b.dataset.del)));
}

async function delImage(id) {
  if (!confirm('确认删除镜像 ' + id + '（含文件）？')) return;
  try {
    await api('/api/images/' + encodeURIComponent(id), { method: 'DELETE', headers: authHeaders() });
    toast('已删除 ' + id); loadImages();
  } catch (e) { toast('删除失败: ' + e.message, true); }
}

async function loadNodes() {
  const d = await api('/api/nodes');
  S.nodes = d.nodes || [];
  document.getElementById('nodes-summary').textContent = `共 ${d.total} 台，在线 ${d.online} 台`;
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
  if (!list.length) { tb.innerHTML = '<tr><td colspan="9">无匹配节点</td></tr>'; return; }
  tb.innerHTML = list.map((n, i) => {
    const st = n.status === 'online' ? '<span class="badge on">在线</span>' : '<span class="badge off">离线</span>';
    const lr = n.last_result ? (n.last_result === 'completed'
      ? '<span class="badge ok">成功</span>'
      : `<span class="badge ${n.last_result === 'failed' ? 'no' : 'warn'}">${n.last_result}</span>`) : '-';
    const main = `<tr class="clickable" data-idx="${i}">
      <td>${st}</td><td>${n.ip || '-'}</td><td>${n.arch || '-'}</td><td>${n.boot_mode || '-'}</td>
      <td class="mono">${(n.uuid || '').slice(0, 8)}</td><td class="mono">${n.mac || '-'}</td>
      <td>${n.deployed_ever ? '是' : '否'}</td><td>${lr}</td><td>${fmtTime(n.last_seen)}</td></tr>`;
    const deploys = (n.deploys || []).map(dp =>
      `镜像 ${dp.image_id} → ${dp.target_disk}　${dp.result}　${humanSize(dp.bytes_written)}　${fmtTime(dp.started_at)}~${fmtTime(dp.ended_at)}${dp.error ? '　错误:' + dp.error : ''}`
    ).join('\n') || '（未部署过）';
    const detail = `<tr class="row-detail" id="rd-${i}" style="display:none"><td colspan="9"><div class="mono">UUID: ${n.uuid}\n内核: ${n.kernel_version || '-'}  Alpine: ${n.alpine_version || '-'}  Agent: ${n.agent_version || '-'}\n首次: ${fmtTime(n.first_seen)}\n--- 部署历史 ---\n${deploys}</div></td></tr>`;
    return main + detail;
  }).join('');
  tb.querySelectorAll('tr.clickable').forEach(tr => tr.addEventListener('click', () => {
    const rd = document.getElementById('rd-' + tr.dataset.idx);
    if (rd) rd.style.display = rd.style.display === 'none' ? '' : 'none';
  }));
}

// ---- 添加镜像 ----
function openAdd() { document.getElementById('add-modal').classList.add('show'); document.getElementById('add-progress').textContent = ''; }
function closeAdd() { document.getElementById('add-modal').classList.remove('show'); if (S.jobTimer) clearInterval(S.jobTimer); }
function onModeChange() {
  const m = document.getElementById('add-mode').value;
  document.getElementById('src-url-l').style.display = (m === 'upload') ? 'none' : '';
  document.getElementById('src-file-l').style.display = (m === 'upload') ? '' : 'none';
  document.getElementById('src-url-l').querySelector('input').placeholder =
    (m === 'path') ? '/data/http/images/上传区/xxx.qcow2' : 'https://.../xxx.qcow2';
}
async function submitAdd() {
  const mode = document.getElementById('add-mode').value;
  const meta = {
    id: document.getElementById('add-id').value.trim(),
    name: document.getElementById('add-name').value.trim(),
    os: document.getElementById('add-os').value.trim(),
    version: document.getElementById('add-version').value.trim(),
    architecture: document.getElementById('add-arch').value,
    firmware: document.getElementById('add-fw').value.split(','),
  };
  try {
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
  const t = prompt('输入管理口令（用于增删镜像等操作）：', token());
  if (t !== null) { sessionStorage.setItem('portal_token', t.trim()); toast('口令已保存到本会话'); }
}

function bind() {
  document.getElementById('token-btn').addEventListener('click', setToken);
  document.getElementById('add-image-btn').addEventListener('click', openAdd);
  document.getElementById('add-cancel').addEventListener('click', closeAdd);
  document.getElementById('add-x').addEventListener('click', closeAdd);
  document.getElementById('add-submit').addEventListener('click', submitAdd);
  document.getElementById('add-mode').addEventListener('change', onModeChange);
  document.getElementById('reload-nodes').addEventListener('click', loadNodes);
  document.getElementById('node-filter').addEventListener('change', (e) => { S.filter = e.target.value; renderNodes(); });
}

function startClock() {
  setInterval(() => { document.getElementById('clock').textContent = new Date().toLocaleString(); }, 1000);
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

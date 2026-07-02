// BootSeed 前端逻辑(无框架,纯原生 JS).
'use strict';

const state = {
  image: null, disk: null, context: null, evtSource: null, clock: null, autorefresh: null,
  deployStatus: null, deploySubmitting: false, deployRunning: false, deployDone: false,
};

function api(path, opts) {
  return fetch(path, opts).then(async (r) => {
    const text = await r.text();
    let data = {};
    try { data = text ? JSON.parse(text) : {}; } catch (e) { /* 非 JSON */ }
    if (!r.ok) throw new Error(data.error || ('HTTP ' + r.status));
    return data;
  });
}

function toast(msg, isError) {
  const el = document.createElement('div');
  el.className = 'toast-item' + (isError ? ' error' : '');
  el.textContent = msg;
  document.getElementById('toast').appendChild(el);
  setTimeout(() => el.remove(), 6000);
}

function humanSize(n) {
  if (!n) return '-';
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0, v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return v.toFixed(1) + ' ' + u[i];
}

// 秒 -> "Xh Ym Zs"
function fmtDuration(sec) {
  sec = Math.max(0, Math.floor(sec || 0));
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  return (h ? h + 'h ' : '') + (h || m ? m + 'm ' : '') + s + 's';
}

function badge(ok, yes, no) {
  return `<span class="badge ${ok ? 'ok' : 'no'}">${ok ? (yes || '是') : (no || '否')}</span>`;
}
function kvRow(key, value) {
  return `<div class="kv-item"><span class="kv-key">${key}:</span><span class="kv-value">${value || '-'}</span></div>`;
}
function renderClock() {
  const ct = document.getElementById('cur-time');
  if (ct) ct.textContent = new Date().toLocaleString();
  const up = document.getElementById('uptime');
  if (up && state.uptimeBase != null) {
    up.textContent = fmtDuration(state.uptimeBase + (Date.now() - state.uptimeAt) / 1000);
  }
}

async function loadContext() {
  const c = await api('/api/context');
  state.context = c;
  state.uptimeBase = c.uptime_seconds || 0;
  state.uptimeAt = Date.now();
  const g = document.getElementById('context-grid');
  const mem = (c.mem_total_bytes)
    ? `${humanSize(c.mem_available_bytes)} 可用 / ${humanSize(c.mem_total_bytes)}` : '-';
  // [label, value] -- 固定 2 列;当前时间/运行时长用 span id 由时钟实时刷新
  const rows = [
    ['节点架构', c.node_architecture], ['启动模式', c.boot_mode],
    ['任务状态', c.task_state], ['Alpine 版本', c.alpine_version],
    ['内核版本', c.kernel_version], ['Agent 版本', c.agent_version],
    ['节点 IP', c.node_ip], ['子网掩码', c.node_netmask],
    ['默认网关', c.node_gateway], ['DNS', (c.node_dns || []).join(', ')],
    ['内存', mem], ['系统启动时间', (c.boot_time || '').replace('T', ' ').replace(/\+.*/, '')],
    ['当前时间', `<span id="cur-time">-</span>`], ['运行时长', `<span id="uptime">-</span>`],
    ['MAC', c.node_mac], ['部署服务端', c.deploy_server],
    ['UUID', c.node_uuid],
  ];
  if (c.runtime_architecture && c.runtime_architecture !== c.node_architecture) {
    rows.splice(1, 0, ['运行架构', c.runtime_architecture]);
  }
  if (c.uname_architecture && c.uname_architecture !== c.node_architecture &&
      c.uname_architecture !== c.runtime_architecture) {
    rows.splice(2, 0, ['uname 架构', c.uname_architecture]);
  }
  g.innerHTML = rows.map(([k, v]) => kvRow(k, v)).join('');
  renderClock();
}

// 本地时钟:当前时间每秒跳动;运行时长按基准 + 已过秒数递增.
function startClock() {
  if (state.clock) return;
  renderClock();
  state.clock = setInterval(renderClock, 1000);
}

async function loadHardware() {
  const h = await api('/api/hardware');
  const nic = document.querySelector('#nic-table tbody');
  nic.innerHTML = (h.interfaces || []).map(n => `<tr>
    <td>${n.name}</td><td class="mono">${n.mac || '-'}</td><td>${n.state}</td>
    <td>${n.driver || '-'}</td><td>${n.firmware || '-'}</td>
    <td class="mono">${n.pci_id || n.platform_id || '-'}</td></tr>`).join('') ||
    '<tr><td colspan="6">未发现网卡</td></tr>';
  const ctrl = document.querySelector('#ctrl-table tbody');
  ctrl.innerHTML = (h.storage_controllers || []).map(s => `<tr>
    <td class="mono">${s.pci_id || '-'}</td><td>${s.class}</td><td>${s.vendor}</td>
    <td>${s.device}</td><td>${s.driver || '-'}</td></tr>`).join('') ||
    '<tr><td colspan="5">未发现存储控制器</td></tr>';
  const unbound = (h.unbound_devices || []).map(u => `未绑定: ${u.path} ${u.pci_id || ''} ${u.class}`);
  const warns = (h.dmesg_warnings || []).map(x => 'dmesg: ' + x);
  document.getElementById('unbound').textContent =
    unbound.concat(warns).join('\n') || '无异常';
}

async function loadImages() {
  const data = await api('/api/images');
  const showAll = document.getElementById('show-incompat').checked;
  const tb = document.querySelector('#image-table tbody');
  tb.innerHTML = '';
  (data.images || []).forEach(img => {
    if (!img.compatible && !showAll) return;
    const tr = document.createElement('tr');
    if (!img.compatible) tr.className = 'incompat';
    tr.innerHTML = `
      <td>${img.compatible ? `<input type="radio" name="image" value="${img.id}">` : '-'}</td>
      <td>${img.name}</td><td>${img.os}</td><td>${img.version}</td>
      <td>${img.architecture}</td><td>${(img.firmware || []).join('/')}</td>
      <td>${img.format}</td><td>${humanSize(img.compressed_size)}</td>
      <td>${humanSize(img.raw_size)}</td>
      <td>${img.compatible ? badge(true) : `<span class="badge no" title="${img.incompatible_reason || ''}">不兼容</span>`}</td>`;
    tb.appendChild(tr);
    const radio = tr.querySelector('input');
    if (radio) radio.addEventListener('change', () => { state.image = img; updateConfirm(); });
  });
  if (!tb.children.length) tb.innerHTML = '<tr><td colspan="10">无可用镜像</td></tr>';
}

async function loadDisks() {
  const data = await api('/api/disks');
  const tb = document.querySelector('#disk-table tbody');
  tb.innerHTML = '';
  (data.disks || []).forEach(d => {
    const target = d.stable_path || d.path;
    const tr = document.createElement('tr');
    tr.innerHTML = `
      <td>${d.allowed ? `<input type="radio" name="disk" value="${target}">` : '-'}</td>
      <td class="mono">${d.path}</td><td class="mono">${d.stable_path || '-'}</td>
      <td>${d.model || '-'}</td><td class="mono">${d.serial || '-'}</td>
      <td>${humanSize(d.size)}</td><td>${d.tran || '-'}</td>
      <td>${d.rotational ? 'HDD' : 'SSD'}</td>
      <td>${d.raid_info ? badge(true, 'RAID') : '-'}</td>
      <td>${d.multipath_top ? badge(false, '', 'mpath') : '-'}</td>
      <td>${d.san_risk ? '<span class="badge warn">SAN</span>' : '-'}</td>
      <td>${d.allowed ? badge(true) : `<span class="badge no" title="${d.reason || ''}">禁止</span>`}</td>`;
    tb.appendChild(tr);
    const radio = tr.querySelector('input');
    if (radio) radio.addEventListener('change', () => {
      state.disk = { target, model: d.model, serial: d.serial, size: d.size };
      updateConfirm();
    });
  });
  if (!tb.children.length) tb.innerHTML = '<tr><td colspan="12">未发现磁盘</td></tr>';
}

function updateConfirm() {
  const s = document.getElementById('confirm-summary');
  if (!state.image || !state.disk) {
    s.textContent = '请选择镜像与目标磁盘.';
    updateDeployButtons();
    return;
  }
  s.textContent =
    `镜像:${state.image.name} (${state.image.architecture})\n` +
    `节点架构:${state.context.node_architecture}\n` +
    `目标磁盘:${state.disk.target}\n` +
    `型号:${state.disk.model || '-'}  序列号:${state.disk.serial || '-'}\n` +
    `容量:${humanSize(state.disk.size)}  镜像解压大小:${humanSize(state.image.raw_size)}`;
  updateDeployButtons();
}

function terminalDeployStage(stage) {
  return ['completed', 'failed', 'cancelled'].includes(stage || '');
}

function deployStage(status) {
  if (!status) return '';
  const taskState = status.task && status.task.state;
  if (terminalDeployStage(taskState)) return taskState;
  return taskState || (status.progress && status.progress.stage) || '';
}

function isDeployRunning(status) {
  if (!status) return Boolean(state.deployRunning);
  const stage = deployStage(status);
  if (terminalDeployStage(stage)) return false;
  if (typeof status.running === 'boolean') return status.running;
  return Boolean(status.active) && !['', 'idle'].includes(stage);
}

function updateDeployButtons() {
  const deployBtn = document.getElementById('deploy-btn');
  const cancelBtn = document.getElementById('cancel-btn');
  const eraseOK = document.getElementById('confirm-input').value === 'ERASE';
  const ready = Boolean(state.image && state.disk && eraseOK);
  const running = isDeployRunning(state.deployStatus);
  cancelBtn.disabled = !running;
  if (state.deploySubmitting) {
    deployBtn.disabled = true;
    deployBtn.textContent = '提交中...';
    return;
  }
  if (running) {
    deployBtn.disabled = true;
    deployBtn.textContent = '部署进行中';
    return;
  }
  deployBtn.disabled = !ready;
  deployBtn.textContent = state.deployDone ? '再次部署' : '开始部署';
}

async function startDeploy() {
  if (state.deploySubmitting || isDeployRunning(state.deployStatus)) return;
  if (!confirm('确认擦除并部署?此操作不可恢复.')) return;
  try {
    state.deploySubmitting = true;
    state.deployDone = false;
    // 隐藏上一轮遗留的"部署完成,立即重启"按钮,避免在新部署运行中还亮着(误导 + 409)。
    const pr = document.getElementById('post-reboot');
    if (pr) pr.style.display = 'none';
    updateDeployButtons();
    await api('/api/deploy', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        image_id: state.image.id,
        target_disk: state.disk.target,
        confirmation: 'ERASE',
        verify_raw: document.getElementById('verify-raw').checked,
        auto_reboot: document.getElementById('auto-reboot').checked,
      }),
    });
    toast('部署任务已启动');
    subscribeEvents();
  } catch (e) {
    state.deploySubmitting = false;
    updateDeployButtons();
    toast('部署失败: ' + e.message, true);
  }
}

function subscribeEvents() {
  if (state.evtSource) state.evtSource.close();
  state.deployDone = false;
  state.deployRunning = true;
  state.deploySubmitting = false;
  state.deployStatus = { active: true, running: true, task: { state: 'preparing' } };
  updateDeployButtons();
  state.lastDl = 0; state.lastDlT = 0;
  const es = new EventSource('/api/deploy/events');
  state.evtSource = es;
  es.onmessage = (ev) => {
    const p = JSON.parse(ev.data);
    // 下载速度:由相邻两帧的 downloaded_bytes 增量估算(后端只统计累计下载量).
    const now = Date.now();
    let dlSpeed = 0;
    if (state.lastDlT && now > state.lastDlT && p.downloaded_bytes >= state.lastDl) {
      dlSpeed = (p.downloaded_bytes - state.lastDl) * 1000 / (now - state.lastDlT);
    }
    state.lastDl = p.downloaded_bytes; state.lastDlT = now;
    document.getElementById('progress-fill').style.width = (p.percent || 0) + '%';
    document.getElementById('progress-text').textContent =
      `阶段: ${p.stage}  ${p.message || ''}\n` +
      `下载: ${humanSize(p.downloaded_bytes)}  下载速度: ${humanSize(dlSpeed)}/s\n` +
      `写入: ${humanSize(p.written_bytes)} / ${humanSize(p.total_bytes)}\n` +
      `写入速度: ${humanSize(p.speed_bps)}/s  平均写入: ${humanSize(p.average_bps)}/s\n` +
      `已用: ${(p.elapsed_seconds || 0).toFixed(0)}s  预计剩余: ${(p.eta_seconds || 0).toFixed(0)}s` +
      (p.error ? `\n错误: ${p.error}` : '');
    if (['completed', 'failed', 'cancelled'].includes(p.stage) || p.error) {
      state.deployDone = true;
      state.deployRunning = false;
      state.deployStatus = {
        active: true,
        running: false,
        task: { state: p.stage },
        progress: p,
      };
      es.close();
      updateDeployButtons();
      // 部署成功后给出醒目的"立即重启"入口
      const pr = document.getElementById('post-reboot');
      if (pr) pr.style.display = (p.stage === 'completed') ? 'inline-block' : 'none';
      loadContext();
      toast(p.stage === 'completed' ? '部署完成,可点击「立即重启」启动新系统'
        : ('部署结束: ' + p.stage), p.stage !== 'completed');
    }
  };
  // 仅在部署已结束时关闭;否则让浏览器自动重连,避免瞬时断连导致进度停更.
  es.onerror = () => { if (state.deployDone) es.close(); };
}

async function post(path, okMsg) {
  try { await api(path, { method: 'POST' }); toast(okMsg); }
  catch (e) { toast(e.message, true); }
}

function bind() {
  document.getElementById('show-incompat').addEventListener('change', loadImages);
  document.getElementById('reload-images').addEventListener('click', async () => {
    try { await api('/api/images/reload', { method: 'POST' }); await loadImages(); toast('清单已刷新'); }
    catch (e) { toast(e.message, true); }
  });
  document.getElementById('reload-disks').addEventListener('click', loadDisks);
  document.getElementById('confirm-input').addEventListener('input', updateDeployButtons);
  document.getElementById('deploy-btn').addEventListener('click', startDeploy);
  document.getElementById('cancel-btn').addEventListener('click', () => post('/api/deploy/cancel', '已请求取消'));
  document.getElementById('reboot-btn').addEventListener('click', () => {
    const running = isDeployRunning(state.deployStatus);
    const msg = running
      ? '当前有部署正在进行,重启会自动取消该部署(已写数据将 fsync 到目标盘)。确认?'
      : '确认重启节点?';
    if (confirm(msg)) post('/api/reboot', '正在重启');
  });
  document.getElementById('poweroff-btn').addEventListener('click', () => {
    const running = isDeployRunning(state.deployStatus);
    const msg = running
      ? '当前有部署正在进行,关机会自动取消该部署(已写数据将 fsync 到目标盘)。确认?'
      : '确认关机?';
    if (confirm(msg)) post('/api/poweroff', '正在关机');
  });
  const pr = document.getElementById('post-reboot');
  if (pr) pr.addEventListener('click', () => {
    if (confirm('部署已完成,确认重启进入新系统?')) post('/api/reboot', '正在重启');
  });
}

// 页面加载/刷新时,若已有部署在进行,自动恢复进度显示.
async function resumeIfDeploying() {
  try {
    const s = await api('/api/deploy/status');
    state.deployStatus = s;
    const stage = deployStage(s);
    const running = isDeployRunning(s);
    state.deployDone = terminalDeployStage(stage);
    state.deployRunning = running;
    updateDeployButtons();
    if (running) {
      subscribeEvents();
      toast('检测到正在进行的部署,已恢复进度显示');
    }
  } catch (e) { /* 忽略 */ }
}

async function init() {
  bind();
  startClock();
  try {
    await loadContext();
    await Promise.all([loadHardware(), loadImages(), loadDisks()]);
    await resumeIfDeploying();
  } catch (e) { toast('初始化失败: ' + e.message, true); }
  // 定时自动刷新节点信息与磁盘(部署进行中由 SSE 驱动,不重复拉取磁盘).
  state.autorefresh = setInterval(async () => {
    try {
      await loadContext();
      if (!state.evtSource || state.deployDone) await loadDisks();
    } catch (e) { /* 忽略瞬时失败 */ }
  }, 12000);
}

init();

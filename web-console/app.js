const storeKeys = {
  linkserverUrl: "emunet.console.linkserverUrl",
  namespace: "emunet.console.namespace",
  emunetName: "emunet.console.emunetName",
  imageGroups: "emunet.console.imageGroups",
};

const defaultImageGroups = [{ image: "100.75.179.29:5000/busybox-ping:1.36", replicas: 2 }];

const el = {
  linkserverUrl: document.querySelector("#linkserver-url"),
  saveConfig: document.querySelector("#save-config"),
  checkHealth: document.querySelector("#check-health"),
  healthPill: document.querySelector("#health-pill"),
  namespace: document.querySelector("#namespace"),
  emunetName: document.querySelector("#emunet-name"),
  emunetStatus: document.querySelector("#emunet-status"),
  refreshEmuNets: document.querySelector("#refresh-emunets"),
  imageGroups: document.querySelector("#image-groups"),
  addGroup: document.querySelector("#add-group"),
  startEmuNet: document.querySelector("#start-emunet"),
  stopEmuNet: document.querySelector("#stop-emunet"),
  emunetList: document.querySelector("#emunet-list"),
  refreshPods: document.querySelector("#refresh-pods"),
  prevPods: document.querySelector("#prev-pods"),
  nextPods: document.querySelector("#next-pods"),
  podPageSize: document.querySelector("#pod-page-size"),
  autoRefresh: document.querySelector("#auto-refresh"),
  podSummary: document.querySelector("#pod-summary"),
  podTable: document.querySelector("#pod-table"),
  metricDesired: document.querySelector("#metric-desired"),
  metricReady: document.querySelector("#metric-ready"),
  metricMac: document.querySelector("#metric-mac"),
  metricNodes: document.querySelector("#metric-nodes"),
  pod1: document.querySelector("#pod1"),
  pod2: document.querySelector("#pod2"),
  throttle: document.querySelector("#throttle"),
  delay: document.querySelector("#delay"),
  loss: document.querySelector("#loss"),
  jitter: document.querySelector("#jitter"),
  applyRule: document.querySelector("#apply-rule"),
  deleteRule: document.querySelector("#delete-rule"),
  ruleStatus: document.querySelector("#rule-status"),
  pingCount: document.querySelector("#ping-count"),
  pingTimeout: document.querySelector("#ping-timeout"),
  runPing: document.querySelector("#run-ping"),
  pingStatus: document.querySelector("#ping-status"),
  pingOutput: document.querySelector("#ping-output"),
};

let pods = [];
let emunets = [];
let refreshTimer = null;
let detailLoaded = false;
let stopProgressToken = 0;
let stopProgressActive = false;
let podPage = {
  offset: 0,
  limit: 100,
  total: 0,
};

function init() {
  const sameOriginUrl = window.location.protocol.startsWith("http") ? window.location.origin : el.linkserverUrl.value;
  el.linkserverUrl.value = localStorage.getItem(storeKeys.linkserverUrl) || sameOriginUrl;
  el.namespace.value = localStorage.getItem(storeKeys.namespace) || el.namespace.value;
  el.emunetName.value = localStorage.getItem(storeKeys.emunetName) || el.emunetName.value;

  renderImageGroups(loadImageGroups());

  el.saveConfig.addEventListener("click", saveConfig);
  el.checkHealth.addEventListener("click", checkHealth);
  el.refreshEmuNets.addEventListener("click", refreshEmuNets);
  el.addGroup.addEventListener("click", () => {
    renderImageGroups([...readImageGroups({ allowEmptyImage: true }), { image: "", replicas: 1 }]);
  });
  el.startEmuNet.addEventListener("click", startEmuNet);
  el.stopEmuNet.addEventListener("click", stopEmuNet);
  el.refreshPods.addEventListener("click", () => refreshPods({ resetPage: true }));
  el.prevPods.addEventListener("click", () => changePodPage(-1));
  el.nextPods.addEventListener("click", () => changePodPage(1));
  el.podPageSize.addEventListener("change", () => {
    podPage.limit = numberValue(el.podPageSize) || 100;
    refreshPods({ resetPage: true });
  });
  el.autoRefresh.addEventListener("change", configureAutoRefresh);
  el.applyRule.addEventListener("click", applyRule);
  el.deleteRule.addEventListener("click", deleteRule);
  el.runPing.addEventListener("click", runPing);

  saveConfig();
  checkHealth();
  refreshEmuNets();
  refreshSummary();
  configureAutoRefresh();
}

function saveConfig() {
  localStorage.setItem(storeKeys.linkserverUrl, baseUrl());
  localStorage.setItem(storeKeys.namespace, el.namespace.value.trim());
  localStorage.setItem(storeKeys.emunetName, el.emunetName.value.trim());
  localStorage.setItem(storeKeys.imageGroups, JSON.stringify(readImageGroups({ allowEmptyImage: true })));
  setPill(el.healthPill, "配置已保存", "muted");
}

function baseUrl() {
  return el.linkserverUrl.value.trim().replace(/\/+$/, "");
}

async function api(path, options = {}) {
  const response = await fetch(`${baseUrl()}${path}`, {
    headers: {
      "Content-Type": "application/json",
      ...(options.headers || {}),
    },
    ...options,
  });
  const text = await response.text();
  let payload = null;
  try {
    payload = text ? JSON.parse(text) : null;
  } catch {
    payload = { raw: text };
  }
  if (!response.ok || payload?.success === false) {
    throw new Error(payload?.error || payload?.raw || `HTTP ${response.status}`);
  }
  return payload;
}

async function checkHealth() {
  setPill(el.healthPill, "检测中", "muted");
  try {
    await api("/api/v1/health");
    setPill(el.healthPill, "LinkServer 正常", "ok");
  } catch (err) {
    setPill(el.healthPill, shortError(err), "bad");
  }
}

async function refreshEmuNets() {
  saveConfig();
  el.emunetStatus.textContent = "正在读取实例";
  try {
    const payload = await api("/api/v1/emunets");
    emunets = Array.isArray(payload.data) ? payload.data : [];
    renderEmuNetList();
    updateSelectedStatus();
  } catch (err) {
    emunets = [];
    el.emunetStatus.textContent = shortError(err);
    renderEmuNetList();
  }
}

async function startEmuNet() {
  const imageGroups = readImageGroups();
  if (!imageGroups.length) {
    el.emunetStatus.textContent = "至少需要一个镜像组";
    return;
  }

  const body = {
    namespace: el.namespace.value.trim() || "default",
    name: el.emunetName.value.trim(),
    totalReplicas: imageGroups.reduce((sum, group) => sum + group.replicas, 0),
    imageGroups,
    selector: { app: "emunet-pod" },
  };
  if (!body.name) {
    el.emunetStatus.textContent = "Name 不能为空";
    return;
  }

  el.startEmuNet.disabled = true;
  el.emunetStatus.textContent = "正在提交启动/更新";
  try {
    const payload = await api("/api/v1/emunets", {
      method: "POST",
      body: JSON.stringify(body),
    });
    el.emunetStatus.textContent = payload.data?.status === "created" ? "实例已创建" : "实例已更新";
    saveConfig();
    await refreshEmuNets();
    clearPodDetails("Pod 详情未加载；自动刷新只更新摘要");
    await refreshSummary();
  } catch (err) {
    el.emunetStatus.textContent = shortError(err);
  } finally {
    el.startEmuNet.disabled = false;
  }
}

async function stopEmuNet() {
  const ns = encodeURIComponent(el.namespace.value.trim() || "default");
  const name = encodeURIComponent(el.emunetName.value.trim());
  if (!name) {
    el.emunetStatus.textContent = "Name 不能为空";
    return;
  }

  el.stopEmuNet.disabled = true;
  el.emunetStatus.textContent = "正在关闭";
  try {
    const payload = await api(`/api/v1/emunets/${ns}/${name}/stop`, { method: "POST" });
    const deletingPods = Number(payload.data?.deletingPods || 0);
    startStopProgress(ns, name, deletingPods);
    await refreshEmuNets();
  } catch (err) {
    stopProgressActive = false;
    el.emunetStatus.textContent = shortError(err);
  } finally {
    el.stopEmuNet.disabled = false;
  }
}

function startStopProgress(ns, name, initialPods) {
  const token = ++stopProgressToken;
  stopProgressActive = true;
  const totalPods = Math.max(0, Number(initialPods || 0));
  renderStopProgress(totalPods, totalPods);
  monitorStopProgress(ns, name, totalPods, token);
}

async function monitorStopProgress(ns, name, totalPods, token) {
  for (let attempt = 0; attempt < 120; attempt++) {
    if (token !== stopProgressToken) {
      return;
    }
    try {
      const payload = await api(`/api/v1/emunets/${ns}/${name}/delete-status`);
      const remainingPods = Number(payload.data?.remainingPods || 0);
      if (remainingPods <= 0) {
        renderStopProgress(0, totalPods);
        el.emunetStatus.textContent = "实例已关闭";
        stopProgressActive = false;
        await refreshEmuNets();
        return;
      }
      renderStopProgress(remainingPods, Math.max(totalPods, remainingPods));
    } catch {
      if (token === stopProgressToken) {
        stopProgressActive = false;
        el.emunetStatus.textContent = "删除请求已提交，暂时无法读取进度";
      }
      return;
    }
    await sleep(1000);
  }
  if (token === stopProgressToken) {
    stopProgressActive = false;
    el.emunetStatus.textContent = "删除仍在进行，请稍后刷新实例";
  }
}

function renderStopProgress(remainingPods, totalPods) {
  const deletedPods = Math.max(0, totalPods - remainingPods);
  const text =
    totalPods > 0
      ? `实例关闭中：已删除 ${deletedPods}/${totalPods} 个 Pod，剩余 ${remainingPods}`
      : "实例关闭中：等待 Kubernetes 清理 Pod";
  el.emunetStatus.textContent = remainingPods <= 0 && totalPods > 0 ? `实例已关闭：已删除 ${totalPods}/${totalPods} 个 Pod` : text;
  el.podSummary.textContent = text;
  el.metricDesired.textContent = String(totalPods);
  el.metricReady.textContent = String(remainingPods);
  el.metricMac.textContent = "-";
  el.metricNodes.textContent = "-";
  el.podTable.innerHTML = `<tr><td colspan="6" class="empty">${escapeHtml(text)}</td></tr>`;
}

async function refreshPods(options = {}) {
  saveConfig();
  const ns = encodeURIComponent(el.namespace.value.trim() || "default");
  const name = encodeURIComponent(el.emunetName.value.trim());
  if (!name) {
    el.podSummary.textContent = "请选择 EmuNet";
    return;
  }
  if (options.resetPage) {
    podPage.offset = 0;
  }
  podPage.limit = numberValue(el.podPageSize) || podPage.limit || 100;
  el.podSummary.textContent = "正在读取 Pod 状态";
  try {
    const payload = await api(`/api/v1/emunets/${ns}/${name}/pods?offset=${podPage.offset}&limit=${podPage.limit}`);
    const data = payload.data || {};
    if (Array.isArray(data)) {
      pods = data;
      podPage.total = data.length;
      podPage.offset = 0;
      podPage.limit = data.length || podPage.limit;
    } else {
      pods = Array.isArray(data.items) ? data.items : [];
      podPage.total = Number(data.total || 0);
      podPage.offset = Number(data.offset || 0);
      podPage.limit = Number(data.limit || podPage.limit);
    }
    detailLoaded = true;
    renderPods();
    renderPodSelects();
    updateSelectedStatus();
  } catch (err) {
    clearPodDetails(shortError(err));
    el.podSummary.textContent = shortError(err);
  }
}

async function refreshSummary() {
  if (stopProgressActive) {
    return;
  }
  saveConfig();
  const ns = encodeURIComponent(el.namespace.value.trim() || "default");
  const name = encodeURIComponent(el.emunetName.value.trim());
  if (!name) {
    el.podSummary.textContent = "请选择 EmuNet";
    return;
  }

  try {
    const payload = await api(`/api/v1/emunets/${ns}/${name}/summary`);
    const summary = payload.data || {};
    renderSummary(summary);
    mergeSummaryIntoSelected(summary);
  } catch (err) {
    const selected = findSelectedEmuNet();
    if (selected) {
      renderSummary({
        desiredReplicas: selected.desiredReplicas || selected.totalReplicas || 0,
        readyReplicas: selected.readyReplicas || 0,
      });
      el.podSummary.textContent = "等待 controller 写入摘要";
    } else {
      renderSummary({});
      el.podSummary.textContent = shortError(err);
    }
  }
}

function configureAutoRefresh() {
  if (refreshTimer) {
    clearInterval(refreshTimer);
    refreshTimer = null;
  }
  if (el.autoRefresh.checked) {
    refreshTimer = setInterval(() => {
      refreshSummary();
    }, 5000);
  }
}

function renderSummary(summary) {
  el.metricDesired.textContent = String(summary.desiredReplicas || 0);
  el.metricReady.textContent = String(summary.readyReplicas || 0);
  el.metricMac.textContent = String(summary.macSyncedReplicas || 0);
  el.metricNodes.textContent = String(summary.nodeCount || 0);
  if (summary.desiredReplicas || summary.readyReplicas) {
    const age = summaryAgeText(summary.lastUpdated);
    el.podSummary.textContent = `${summary.readyReplicas || 0}/${summary.desiredReplicas || 0} Ready, ${summary.macSyncedReplicas || 0} MAC synced${age ? `, ${age}` : ""}`;
  }
}

function summaryAgeText(lastUpdated) {
  if (!lastUpdated) {
    return "";
  }
  const updatedAt = Date.parse(lastUpdated);
  if (!Number.isFinite(updatedAt)) {
    return "";
  }
  const ageSeconds = Math.max(0, Math.round((Date.now() - updatedAt) / 1000));
  if (ageSeconds < 60) {
    return `${ageSeconds}s 前更新`;
  }
  return `${Math.floor(ageSeconds / 60)}m${ageSeconds % 60}s 前更新`;
}

function mergeSummaryIntoSelected(summary) {
  if (!summary?.name || !summary?.namespace) {
    return;
  }
  const selected = emunets.find((item) => item.namespace === summary.namespace && item.name === summary.name);
  if (!selected) {
    return;
  }
  selected.readyReplicas = Number(summary.readyReplicas || 0);
  selected.desiredReplicas = Number(summary.desiredReplicas || 0);
  selected.totalReplicas = Number(summary.desiredReplicas || selected.totalReplicas || 0);
  selected.observedGen = Number(summary.observedGen || selected.observedGen || 0);
  renderEmuNetList();
  updateSelectedStatus();
}

function clearPodDetails(message) {
  pods = [];
  detailLoaded = false;
  podPage.offset = 0;
  podPage.total = 0;
  el.metricMac.textContent = "-";
  el.metricNodes.textContent = "-";
  el.podSummary.textContent = message;
  el.podTable.innerHTML = `<tr><td colspan="6" class="empty">点击“刷新详情”加载 Pod 表</td></tr>`;
  renderPodSelects();
  updatePodPager();
}

function renderPods() {
  const ready = pods.filter((pod) => pod.ready).length;
  const withMac = pods.filter((pod) => pod.macAddress).length;
  const nodes = new Set(pods.map((pod) => pod.nodeName).filter(Boolean)).size;
  const selected = findSelectedEmuNet();
  const desired = selected?.desiredReplicas || selected?.totalReplicas || pods.length;

  el.metricDesired.textContent = String(desired);
  el.metricReady.textContent = String(ready);
  el.metricMac.textContent = String(withMac);
  el.metricNodes.textContent = String(nodes);

  if (!pods.length) {
    el.podSummary.textContent = "暂无 Pod 数据";
    el.podTable.innerHTML = `<tr><td colspan="6" class="empty">暂无数据</td></tr>`;
    updatePodPager();
    return;
  }

  const start = podPage.offset + 1;
  const end = podPage.offset + pods.length;
  el.podSummary.textContent = `第 ${start}-${end} / ${podPage.total || pods.length} 个 Pod；本页 ${ready}/${pods.length} Ready, ${withMac}/${pods.length} 已同步 MAC`;
  el.podTable.innerHTML = pods
    .map(
      (pod) => `
        <tr>
          <td class="pod-name">${escapeHtml(pod.podName || "")}</td>
          <td>${phasePill(pod.phase, pod.ready)}</td>
          <td>${escapeHtml(pod.nodeName || "-")}</td>
          <td>${escapeHtml(pod.podIP || "-")}</td>
          <td>${escapeHtml(pod.macAddress || "-")}</td>
          <td>${escapeHtml(String(pod.vethIfIndex || "-"))}</td>
        </tr>
      `,
    )
    .join("");
  updatePodPager();
}

function changePodPage(direction) {
  if (!detailLoaded) {
    refreshPods({ resetPage: true });
    return;
  }
  const nextOffset = podPage.offset + direction * podPage.limit;
  if (nextOffset < 0 || nextOffset >= podPage.total) {
    return;
  }
  podPage.offset = nextOffset;
  refreshPods();
}

function updatePodPager() {
  const hasDetails = detailLoaded && podPage.total > 0;
  el.prevPods.disabled = !hasDetails || podPage.offset <= 0;
  el.nextPods.disabled = !hasDetails || podPage.offset + pods.length >= podPage.total;
}

function renderPodSelects() {
  const usablePods = pods.filter((pod) => pod.podName && pod.nodeName && pod.macAddress && pod.vethIfIndex);
  const options = usablePods
    .map((pod) => `<option value="${escapeHtml(pod.podName)}">${escapeHtml(pod.podName)}</option>`)
    .join("");
  el.pod1.innerHTML = options || `<option value="">无可用 Pod</option>`;
  el.pod2.innerHTML = options || `<option value="">无可用 Pod</option>`;
  if (usablePods[1]) {
    el.pod2.value = usablePods[1].podName;
  }
}

function renderEmuNetList() {
  if (!emunets.length) {
    el.emunetList.innerHTML = `<div class="empty block">暂无实例</div>`;
    return;
  }

  el.emunetList.innerHTML = emunets
    .map((item) => {
      const selected = item.namespace === el.namespace.value.trim() && item.name === el.emunetName.value.trim();
      return `
        <button class="instance-item ${selected ? "selected" : ""}" data-namespace="${escapeHtml(item.namespace)}" data-name="${escapeHtml(item.name)}">
          <span>
            <strong>${escapeHtml(item.namespace)}/${escapeHtml(item.name)}</strong>
            <small>${escapeHtml(describeImageGroups(item.imageGroups || []))}</small>
          </span>
          <span class="pill ${item.readyReplicas === item.totalReplicas && item.totalReplicas > 0 ? "ok" : "warn"}">
            ${escapeHtml(String(item.readyReplicas || 0))}/${escapeHtml(String(item.totalReplicas || 0))}
          </span>
        </button>
      `;
    })
    .join("");

  el.emunetList.querySelectorAll(".instance-item").forEach((button) => {
    button.addEventListener("click", () => selectEmuNet(button.dataset.namespace, button.dataset.name));
  });
}

function selectEmuNet(namespace, name) {
  const selected = emunets.find((item) => item.namespace === namespace && item.name === name);
  el.namespace.value = namespace;
  el.emunetName.value = name;
  if (selected?.imageGroups?.length) {
    renderImageGroups(selected.imageGroups);
  }
  saveConfig();
  renderEmuNetList();
  clearPodDetails("Pod 详情未加载；自动刷新只更新摘要");
  refreshSummary();
}

function updateSelectedStatus() {
  if (stopProgressActive) {
    return;
  }
  const selected = findSelectedEmuNet();
  if (!selected) {
    el.emunetStatus.textContent = "当前实例未创建";
    return;
  }
  el.emunetStatus.textContent = `${selected.readyReplicas || 0}/${selected.totalReplicas || 0} Ready`;
}

function findSelectedEmuNet() {
  return emunets.find(
    (item) => item.namespace === (el.namespace.value.trim() || "default") && item.name === el.emunetName.value.trim(),
  );
}

function renderImageGroups(groups) {
  el.imageGroups.innerHTML = groups
    .map(
      (group, index) => `
        <div class="image-group" data-index="${index}">
          <label>
            <span>镜像</span>
            <input class="group-image" value="${escapeHtml(group.image || "")}" spellcheck="false" />
          </label>
          <label>
            <span>副本数</span>
            <input class="group-replicas" type="number" min="0" step="1" value="${escapeHtml(String(group.replicas ?? 1))}" />
          </label>
          <button class="remove-group" ${groups.length <= 1 ? "disabled" : ""}>删除</button>
        </div>
      `,
    )
    .join("");

  el.imageGroups.querySelectorAll(".remove-group").forEach((button) => {
    button.addEventListener("click", () => {
      const index = Number(button.closest(".image-group").dataset.index);
      const nextGroups = readImageGroups({ allowEmptyImage: true }).filter((_, groupIndex) => groupIndex !== index);
      renderImageGroups(nextGroups.length ? nextGroups : defaultImageGroups);
    });
  });
}

function loadImageGroups() {
  try {
    const saved = JSON.parse(localStorage.getItem(storeKeys.imageGroups) || "null");
    if (Array.isArray(saved) && saved.length) {
      return saved;
    }
  } catch {
    return defaultImageGroups;
  }
  return defaultImageGroups;
}

function readImageGroups(options = {}) {
  return [...el.imageGroups.querySelectorAll(".image-group")]
    .map((group) => ({
      image: group.querySelector(".group-image").value.trim(),
      replicas: numberValue(group.querySelector(".group-replicas")),
    }))
    .filter((group) => (options.allowEmptyImage ? true : group.image));
}

async function applyRule() {
  await submitRule("POST");
}

async function deleteRule() {
  await submitRule("DELETE");
}

async function runPing() {
  const pod1 = el.pod1.value;
  const pod2 = el.pod2.value;
  if (!pod1 || !pod2 || pod1 === pod2) {
    setPill(el.pingStatus, "请选择两个不同 Pod", "warn");
    return;
  }

  setPill(el.pingStatus, "测试中", "muted");
  el.runPing.disabled = true;
  el.pingOutput.textContent = "ping running...";
  try {
    const payload = await api("/api/v1/ping/by-pods", {
      method: "POST",
      body: JSON.stringify({
        namespace: el.namespace.value.trim() || "default",
        pod1,
        pod2,
        count: numberValue(el.pingCount) || 4,
        timeoutSeconds: numberValue(el.pingTimeout) || 2,
      }),
    });
    const data = payload.data || {};
    setPill(el.pingStatus, "测试完成", "ok");
    el.pingOutput.textContent = [
      `$ ${data.sourcePod} -> ${data.targetPod} (${data.targetIP})`,
      data.stdout || "",
      data.stderr ? `stderr:\n${data.stderr}` : "",
    ]
      .filter(Boolean)
      .join("\n");
  } catch (err) {
    setPill(el.pingStatus, "测试失败", "bad");
    el.pingOutput.textContent = err?.message || String(err);
  } finally {
    el.runPing.disabled = false;
  }
}

async function submitRule(method) {
  const pod1 = el.pod1.value;
  const pod2 = el.pod2.value;
  if (!pod1 || !pod2 || pod1 === pod2) {
    setPill(el.ruleStatus, "请选择两个不同 Pod", "warn");
    return;
  }

  const body =
    method === "POST"
      ? {
          pod1,
          pod2,
          throttleRateBps: numberValue(el.throttle),
          delay: numberValue(el.delay),
          lossRate: numberValue(el.loss),
          jitter: numberValue(el.jitter),
        }
      : { pod1, pod2 };

  setPill(el.ruleStatus, method === "POST" ? "应用中" : "删除中", "muted");
  try {
    await api("/api/v1/ebpf/entry/by-pods", {
      method,
      body: JSON.stringify(body),
    });
    setPill(el.ruleStatus, method === "POST" ? "规则已发布" : "删除已发布", "ok");
  } catch (err) {
    setPill(el.ruleStatus, shortError(err), "bad");
  }
}

function setPill(target, text, state) {
  target.textContent = text;
  target.className = `pill ${state}`;
}

function phasePill(phase, ready) {
  const cls = ready ? "ok" : phase === "Running" ? "warn" : "muted";
  return `<span class="pill ${cls}">${escapeHtml(phase || "Unknown")}</span>`;
}

function numberValue(input) {
  const value = Number(input.value);
  return Number.isFinite(value) && value > 0 ? Math.floor(value) : 0;
}

function describeImageGroups(groups) {
  return groups.map((group) => `${group.replicas} x ${group.image}`).join(", ");
}

function shortError(err) {
  const msg = err?.message || String(err);
  return msg.length > 36 ? `${msg.slice(0, 33)}...` : msg;
}

function sleep(ms) {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

init();

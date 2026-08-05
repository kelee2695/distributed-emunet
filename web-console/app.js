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
  topologyRule: document.querySelector("#topology-rule"),
  topologyNodeLimit: document.querySelector("#topology-node-limit"),
  topologyNeighbors: document.querySelector("#topology-neighbors"),
  topologyBaseDelay: document.querySelector("#topology-base-delay"),
  topologyDistanceDelay: document.querySelector("#topology-distance-delay"),
  topologyMaxLoss: document.querySelector("#topology-max-loss"),
  topologyJitterScale: document.querySelector("#topology-jitter-scale"),
  topologyBandwidth: document.querySelector("#topology-bandwidth"),
  topologyDynamicInterval: document.querySelector("#topology-dynamic-interval"),
  topologyMotionScale: document.querySelector("#topology-motion-scale"),
  loadTopologyPods: document.querySelector("#load-topology-pods"),
  previewTopology: document.querySelector("#preview-topology"),
  applyTopology: document.querySelector("#apply-topology"),
  startDynamicTopology: document.querySelector("#start-dynamic-topology"),
  stopDynamicTopology: document.querySelector("#stop-dynamic-topology"),
  clearTopologyRules: document.querySelector("#clear-topology-rules"),
  cancelTopology: document.querySelector("#cancel-topology"),
  topologyCanvas: document.querySelector("#topology-canvas"),
  topologyStatus: document.querySelector("#topology-status"),
  topologySummary: document.querySelector("#topology-summary"),
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
let topologyPods = [];
let topologyNodes = [];
let topologyEdges = [];
let topologyCancelled = false;
let dynamicTopologyTimer = null;
let dynamicTopologyRunning = false;
let dynamicTopologyInFlight = false;
let dynamicTopologyTick = 0;
let dynamicTopologySkipped = 0;

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
  el.loadTopologyPods.addEventListener("click", loadTopologyPods);
  el.previewTopology.addEventListener("click", previewTopology);
  el.applyTopology.addEventListener("click", applyTopology);
  el.startDynamicTopology.addEventListener("click", startDynamicTopology);
  el.stopDynamicTopology.addEventListener("click", () => {
    stopDynamicTopology();
    setPill(el.topologyStatus, "动态已停止", "warn");
  });
  el.clearTopologyRules.addEventListener("click", clearTopologyRules);
  el.cancelTopology.addEventListener("click", () => {
    topologyCancelled = true;
    stopDynamicTopology();
    setPill(el.topologyStatus, "停止中", "warn");
  });
  window.addEventListener("resize", () => {
    if (topologyNodes.length) {
      drawTopology();
    }
  });

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
    clearTopology("实例已更新，请重新加载拓扑节点");
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
    clearTopology("实例正在关闭，拓扑预览已清空");
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

function clearTopology(message) {
  stopDynamicTopology();
  topologyPods = [];
  topologyNodes = [];
  topologyEdges = [];
  topologyCancelled = true;
  drawTopology();
  setPill(el.topologyStatus, "就绪", "muted");
  el.topologySummary.textContent = message || "按空间位置生成链路参数并批量下发";
}

async function loadTopologyPods() {
  saveConfig();
  setPill(el.topologyStatus, "加载中", "muted");
  el.topologySummary.textContent = "正在分页读取 Pod 详情";
  try {
    topologyPods = await fetchAllUsablePods(numberValue(el.topologyNodeLimit) || 200);
    topologyNodes = layoutTopologyNodes(topologyPods, el.topologyRule.value);
    topologyEdges = [];
    drawTopology();
    setPill(el.topologyStatus, "已加载", "ok");
    el.topologySummary.textContent = `已加载 ${topologyPods.length} 个可配置 Pod`;
    return true;
  } catch (err) {
    setPill(el.topologyStatus, shortError(err), "bad");
    el.topologySummary.textContent = err?.message || String(err);
    return false;
  }
}

async function fetchAllUsablePods(limit) {
  const ns = encodeURIComponent(el.namespace.value.trim() || "default");
  const name = encodeURIComponent(el.emunetName.value.trim());
  if (!name) {
    throw new Error("请选择 EmuNet");
  }
  const pageLimit = 500;
  let offset = 0;
  let total = Infinity;
  const result = [];
  while (offset < total && result.length < limit) {
    const payload = await api(`/api/v1/emunets/${ns}/${name}/pods?offset=${offset}&limit=${pageLimit}`);
    const data = payload.data || {};
    const items = Array.isArray(data.items) ? data.items : Array.isArray(data) ? data : [];
    total = Number(data.total || items.length || 0);
    for (const pod of items) {
      if (pod.podName && pod.nodeName && pod.macAddress && pod.vethIfIndex) {
        result.push(pod);
        if (result.length >= limit) {
          break;
        }
      }
    }
    offset += pageLimit;
    if (!items.length) {
      break;
    }
  }
  if (result.length < 2) {
    throw new Error("可配置 Pod 不足 2 个，请等待 MAC 同步");
  }
  return result.sort((a, b) => String(a.podName).localeCompare(String(b.podName)));
}

async function previewTopology() {
  if (!topologyPods.length) {
    const loaded = await loadTopologyPods();
    if (!loaded) {
      return false;
    }
  }
  topologyNodes = layoutTopologyNodes(topologyPods, el.topologyRule.value);
  topologyEdges = buildTopologyEdges(topologyNodes, el.topologyRule.value);
  drawTopology();
  setPill(el.topologyStatus, "预览完成", "ok");
  el.topologySummary.textContent = `${topologyNodes.length} 个节点，${topologyEdges.length} 条链路`;
  return true;
}

function layoutTopologyNodes(podList, rule) {
  const n = podList.length;
  if (rule === "ring") {
    return podList.map((pod, index) => {
      const angle = (2 * Math.PI * index) / n - Math.PI / 2;
      return {
        pod,
        baseX: 0.5 + 0.42 * Math.cos(angle),
        baseY: 0.5 + 0.42 * Math.sin(angle),
        x: 0.5 + 0.42 * Math.cos(angle),
        y: 0.5 + 0.42 * Math.sin(angle),
      };
    });
  }
  if (rule === "nearest") {
    return podList.map((pod) => {
      const h1 = hashText(`${pod.podName}:x`);
      const h2 = hashText(`${pod.podName}:y`);
      return {
        pod,
        baseX: 0.08 + (h1 / 0xffffffff) * 0.84,
        baseY: 0.08 + (h2 / 0xffffffff) * 0.84,
        x: 0.08 + (h1 / 0xffffffff) * 0.84,
        y: 0.08 + (h2 / 0xffffffff) * 0.84,
      };
    });
  }
  const cols = Math.ceil(Math.sqrt(n));
  const rows = Math.ceil(n / cols);
  return podList.map((pod, index) => {
    const col = index % cols;
    const row = Math.floor(index / cols);
    return {
      pod,
      gridCol: col,
      gridRow: row,
      baseX: cols <= 1 ? 0.5 : 0.06 + (col / (cols - 1)) * 0.88,
      baseY: rows <= 1 ? 0.5 : 0.08 + (row / (rows - 1)) * 0.84,
      x: cols <= 1 ? 0.5 : 0.06 + (col / (cols - 1)) * 0.88,
      y: rows <= 1 ? 0.5 : 0.08 + (row / (rows - 1)) * 0.84,
    };
  });
}

function buildTopologyEdges(nodes, rule) {
  if (nodes.length < 2) {
    return [];
  }
  const edges = [];
  if (rule === "ring") {
    for (let i = 0; i < nodes.length; i++) {
      edges.push(makeTopologyEdge(nodes[i], nodes[(i + 1) % nodes.length]));
    }
    return edges;
  }
  if (rule === "nearest") {
    const k = Math.min(numberValue(el.topologyNeighbors) || 4, nodes.length - 1);
    const seen = new Set();
    for (const source of nodes) {
      const nearest = nodes
        .filter((target) => target !== source)
        .map((target) => ({ target, distance: nodeDistance(source, target) }))
        .sort((a, b) => a.distance - b.distance)
        .slice(0, k);
      for (const item of nearest) {
        const key = edgeKey(source, item.target);
        if (!seen.has(key)) {
          seen.add(key);
          edges.push(makeTopologyEdge(source, item.target));
        }
      }
    }
    return edges;
  }
  const byCell = new Map(nodes.map((node) => [`${node.gridCol},${node.gridRow}`, node]));
  for (const node of nodes) {
    for (const [dc, dr] of [
      [1, 0],
      [0, 1],
    ]) {
      const next = byCell.get(`${node.gridCol + dc},${node.gridRow + dr}`);
      if (next) {
        edges.push(makeTopologyEdge(node, next));
      }
    }
  }
  return edges;
}

function makeTopologyEdge(a, b) {
  const distance = nodeDistance(a, b);
  const baseDelay = numberValue(el.topologyBaseDelay);
  const distanceDelay = numberValue(el.topologyDistanceDelay);
  const maxLoss = numberValue(el.topologyMaxLoss);
  const jitterScale = numberValue(el.topologyJitterScale);
  return {
    a,
    b,
    distance,
    params: {
      throttleRateBps: numberValue(el.topologyBandwidth),
      delay: Math.round(baseDelay + distance * distanceDelay),
      lossRate: Math.min(maxLoss, Math.round(distance * distance * maxLoss)),
      jitter: Math.round(distance * jitterScale),
    },
  };
}

async function applyTopology() {
  if (!topologyEdges.length) {
    const ready = await previewTopology();
    if (!ready) {
      return;
    }
  }
  if (topologyEdges.length > 5000) {
    setPill(el.topologyStatus, "链路过多", "bad");
    el.topologySummary.textContent = "一次最多下发 5000 条链路，请降低节点上限或 k 值";
    return;
  }
  topologyCancelled = false;
  el.applyTopology.disabled = true;
  setPill(el.topologyStatus, "下发中", "muted");
  try {
    const result = await runTopologyJobs(topologyEdges, 4, async (edge) => {
      if (topologyCancelled) {
        return false;
      }
      await api("/api/v1/ebpf/entry/by-pods", {
        method: "POST",
        body: JSON.stringify({
          pod1: edge.a.pod.podName,
          pod2: edge.b.pod.podName,
          ...edge.params,
        }),
      });
      return true;
    });
    setPill(el.topologyStatus, topologyCancelled ? "已停止" : "已下发", topologyCancelled ? "warn" : "ok");
    el.topologySummary.textContent = `成功 ${result.success}/${topologyEdges.length} 条，失败 ${result.failed} 条`;
  } catch (err) {
    setPill(el.topologyStatus, shortError(err), "bad");
    el.topologySummary.textContent = err?.message || String(err);
  } finally {
    el.applyTopology.disabled = false;
  }
}

async function startDynamicTopology() {
  if (dynamicTopologyRunning) {
    setPill(el.topologyStatus, "动态运行中", "ok");
    return;
  }
  if (!topologyNodes.length || !topologyEdges.length) {
    const ready = await previewTopology();
    if (!ready) {
      return;
    }
  }
  if (topologyEdges.length > 5000) {
    setPill(el.topologyStatus, "链路过多", "bad");
    el.topologySummary.textContent = "动态模式一次最多刷新 5000 条链路，请降低节点上限或 k 值";
    return;
  }

  dynamicTopologyRunning = true;
  dynamicTopologyTick = 0;
  dynamicTopologySkipped = 0;
  topologyCancelled = false;
  el.startDynamicTopology.disabled = true;
  setPill(el.topologyStatus, "动态运行中", "ok");

  await runDynamicTopologyTick();
  if (!dynamicTopologyRunning) {
    return;
  }
  const intervalMs = Math.max(500, numberValue(el.topologyDynamicInterval) || 1000);
  dynamicTopologyTimer = window.setInterval(runDynamicTopologyTick, intervalMs);
}

function stopDynamicTopology() {
  topologyCancelled = true;
  if (dynamicTopologyTimer) {
    window.clearInterval(dynamicTopologyTimer);
    dynamicTopologyTimer = null;
  }
  dynamicTopologyRunning = false;
  dynamicTopologyInFlight = false;
  if (el.startDynamicTopology) {
    el.startDynamicTopology.disabled = false;
  }
}

async function runDynamicTopologyTick() {
  if (!dynamicTopologyRunning) {
    return;
  }
  if (dynamicTopologyInFlight) {
    dynamicTopologySkipped++;
    el.topologySummary.textContent = `上一轮动态下发未完成，已跳过 ${dynamicTopologySkipped} 次`;
    return;
  }

  dynamicTopologyInFlight = true;
  dynamicTopologyTick++;
  try {
    updateDynamicNodePositions(dynamicTopologyTick);
    topologyEdges = buildTopologyEdges(topologyNodes, el.topologyRule.value);
    if (topologyEdges.length > 5000) {
      throw new Error("动态链路超过 5000 条，请降低节点上限或 k 值");
    }
    drawTopology();
    const result = await runTopologyJobs(
      topologyEdges,
      4,
      async (edge) => {
        if (!dynamicTopologyRunning || topologyCancelled) {
          return false;
        }
        await api("/api/v1/ebpf/entry/by-pods", {
          method: "POST",
          body: JSON.stringify({
            pod1: edge.a.pod.podName,
            pod2: edge.b.pod.podName,
            ...edge.params,
          }),
        });
        return true;
      },
      "动态",
    );
    const sample = topologyNodes[0];
    setPill(el.topologyStatus, dynamicTopologyRunning ? "动态运行中" : "已停止", dynamicTopologyRunning ? "ok" : "warn");
    el.topologySummary.textContent = `第 ${dynamicTopologyTick} 轮：${topologyNodes.length} 节点，${topologyEdges.length} 链路，成功 ${result.success}，失败 ${result.failed}；样例坐标 ${sample.pod.podName}=(${sample.x.toFixed(3)}, ${sample.y.toFixed(3)})`;
  } catch (err) {
    stopDynamicTopology();
    setPill(el.topologyStatus, shortError(err), "bad");
    el.topologySummary.textContent = err?.message || String(err);
  } finally {
    dynamicTopologyInFlight = false;
  }
}

function updateDynamicNodePositions(tick) {
  const scale = Math.min(0.3, numberValue(el.topologyMotionScale) / 100);
  const t = tick * 0.45;
  for (const node of topologyNodes) {
    const hx = hashText(`${node.pod.podName}:motion-x`);
    const hy = hashText(`${node.pod.podName}:motion-y`);
    const phaseX = (hx / 0xffffffff) * Math.PI * 2;
    const phaseY = (hy / 0xffffffff) * Math.PI * 2;
    const ampX = scale * (0.45 + ((hx >>> 8) % 55) / 100);
    const ampY = scale * (0.45 + ((hy >>> 8) % 55) / 100);
    node.x = clamp01((node.baseX ?? node.x) + ampX * Math.sin(t + phaseX), 0.03, 0.97);
    node.y = clamp01((node.baseY ?? node.y) + ampY * Math.cos(t * 0.83 + phaseY), 0.03, 0.97);
  }
}

async function clearTopologyRules() {
  el.clearTopologyRules.disabled = true;
  setPill(el.topologyStatus, "清空中", "muted");
  try {
    const payload = await api("/api/v1/ebpf/entries/clear", { method: "POST" });
    const data = payload.data || {};
    setPill(el.topologyStatus, data.status === "partial" ? "部分发布" : "已清空", data.status === "partial" ? "warn" : "ok");
    el.topologySummary.textContent = `已向 ${data.published || 0}/${data.totalNodes || 0} 个节点发布全局清空命令`;
  } catch (err) {
    setPill(el.topologyStatus, shortError(err), "bad");
    el.topologySummary.textContent = err?.message || String(err);
  } finally {
    el.clearTopologyRules.disabled = false;
  }
}

async function runTopologyJobs(items, concurrency, worker, progressLabel = "下发") {
  let cursor = 0;
  let success = 0;
  let failed = 0;
  async function next() {
    while (cursor < items.length && !topologyCancelled) {
      const index = cursor++;
      try {
        const applied = await worker(items[index]);
        if (applied) {
          success++;
        }
      } catch {
        failed++;
      }
      if ((success + failed) % 20 === 0 || success + failed === items.length) {
        el.topologySummary.textContent = `${progressLabel} ${success + failed}/${items.length} 条，成功 ${success}，失败 ${failed}`;
      }
    }
  }
  await Promise.all(Array.from({ length: Math.min(concurrency, items.length) }, next));
  return { success, failed };
}

function drawTopology() {
  const canvas = el.topologyCanvas;
  const ctx = canvas.getContext("2d");
  const rect = canvas.getBoundingClientRect();
  const dpr = window.devicePixelRatio || 1;
  canvas.width = Math.max(1, Math.floor(rect.width * dpr));
  canvas.height = Math.max(1, Math.floor(rect.height * dpr));
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, rect.width, rect.height);
  ctx.fillStyle = "#f9faf8";
  ctx.fillRect(0, 0, rect.width, rect.height);

  const width = rect.width;
  const height = rect.height;
  ctx.lineWidth = 1;
  ctx.strokeStyle = "rgba(23, 111, 100, 0.22)";
  for (const edge of topologyEdges.slice(0, 6000)) {
    ctx.beginPath();
    ctx.moveTo(edge.a.x * width, edge.a.y * height);
    ctx.lineTo(edge.b.x * width, edge.b.y * height);
    ctx.stroke();
  }

  const radius = topologyNodes.length > 500 ? 2.1 : topologyNodes.length > 200 ? 2.8 : 4;
  for (const node of topologyNodes) {
    ctx.beginPath();
    ctx.fillStyle = node.pod.ready ? "#176f64" : "#9a6500";
    ctx.arc(node.x * width, node.y * height, radius, 0, Math.PI * 2);
    ctx.fill();
  }
}

function nodeDistance(a, b) {
  return Math.hypot(a.x - b.x, a.y - b.y);
}

function edgeKey(a, b) {
  const names = [a.pod.podName, b.pod.podName].sort();
  return `${names[0]}|${names[1]}`;
}

function hashText(text) {
  let hash = 2166136261;
  for (let i = 0; i < text.length; i++) {
    hash ^= text.charCodeAt(i);
    hash = Math.imul(hash, 16777619);
  }
  return hash >>> 0;
}

function clamp01(value, min, max) {
  return Math.max(min, Math.min(max, value));
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
  clearTopology("实例已切换，请重新加载拓扑节点");
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

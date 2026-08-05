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
};

let pods = [];
let emunets = [];
let refreshTimer = null;

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
  el.refreshPods.addEventListener("click", refreshPods);
  el.autoRefresh.addEventListener("change", configureAutoRefresh);
  el.applyRule.addEventListener("click", applyRule);
  el.deleteRule.addEventListener("click", deleteRule);

  saveConfig();
  checkHealth();
  refreshEmuNets();
  refreshPods();
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
    await refreshPods();
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
    await api(`/api/v1/emunets/${ns}/${name}/stop`, { method: "POST" });
    el.emunetStatus.textContent = "关闭请求已提交";
    pods = [];
    renderPods();
    renderPodSelects();
    await refreshEmuNets();
  } catch (err) {
    el.emunetStatus.textContent = shortError(err);
  } finally {
    el.stopEmuNet.disabled = false;
  }
}

async function refreshPods() {
  saveConfig();
  const ns = encodeURIComponent(el.namespace.value.trim() || "default");
  const name = encodeURIComponent(el.emunetName.value.trim());
  if (!name) {
    el.podSummary.textContent = "请选择 EmuNet";
    return;
  }
  el.podSummary.textContent = "正在读取 Pod 状态";
  try {
    const payload = await api(`/api/v1/emunets/${ns}/${name}/pods`);
    pods = Array.isArray(payload.data) ? payload.data : [];
    renderPods();
    renderPodSelects();
    updateSelectedStatus();
  } catch (err) {
    pods = [];
    el.podSummary.textContent = shortError(err);
    renderPods();
    renderPodSelects();
  }
}

function configureAutoRefresh() {
  if (refreshTimer) {
    clearInterval(refreshTimer);
    refreshTimer = null;
  }
  if (el.autoRefresh.checked) {
    refreshTimer = setInterval(() => {
      refreshPods();
      refreshEmuNets();
    }, 5000);
  }
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
    return;
  }

  el.podSummary.textContent = `${ready}/${pods.length} Ready, ${withMac}/${pods.length} 已同步 MAC`;
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
  refreshPods();
}

function updateSelectedStatus() {
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

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#039;");
}

init();

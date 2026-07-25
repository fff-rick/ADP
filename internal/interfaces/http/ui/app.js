const state = {
  token: window.localStorage.getItem("adp.token") || "",
  user: null,
  toastTimer: null,
  refreshTimer: null,
};

const page = document.body.dataset.page || "home";

const elements = {
  clock: byId("clock"),
  sessionState: byId("session-state"),
  toast: byId("toast"),
  loginForm: byId("login-form"),
  loginMessage: byId("login-message"),
  logoutButton: byId("logout-button"),
  metricsGrid: byId("metrics-grid"),
  approvalList: byId("approval-list"),
  auditList: byId("audit-list"),
  userForm: byId("user-form"),
  usersAccessNote: byId("users-access-note"),
  userList: byId("user-list"),
  workerForm: byId("worker-form"),
  workerList: byId("worker-list"),
  jobForm: byId("job-form"),
  jobList: byId("job-list"),
  taskForm: byId("task-form"),
  taskInput: byId("task-input"),
  taskParams: byId("task-params"),
  taskOutput: byId("task-output"),
  taskList: byId("task-list"),
  templateList: byId("template-list"),
  configForm: byId("config-form"),
  configKind: byId("config-kind"),
  configFile: byId("config-file"),
  configYAML: byId("config-yaml"),
  configList: byId("config-list"),
  configRefresh: byId("config-refresh"),
  themeToggle: byId("theme-toggle"),
};

boot();

function boot() {
  startClock();
  initTheme();
  bindCommonEvents();
  updateSessionState();
  renderLoggedOutPlaceholders();
  initScrollReveal();
  if (state.token) {
    refreshCurrentPage();
    state.refreshTimer = window.setInterval(refreshCurrentPage, 15000);
  }
}

/* ── Theme ── */

function initTheme() {
  const saved = window.localStorage.getItem("adp.theme");
  if (saved) {
    document.documentElement.setAttribute("data-theme", saved);
  }
}

function toggleTheme() {
  const current = document.documentElement.getAttribute("data-theme");
  const next = current === "dark" ? "light" : "dark";
  document.documentElement.setAttribute("data-theme", next);
  window.localStorage.setItem("adp.theme", next);
}

/* ── Scroll Reveal ── */

function initScrollReveal() {
  if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
    document.querySelectorAll(".scroll-reveal").forEach(function(el) {
      el.classList.add("scroll-reveal-visible");
    });
    return;
  }

  var observer = new IntersectionObserver(function(entries) {
    entries.forEach(function(entry) {
      if (entry.isIntersecting) {
        entry.target.classList.add("scroll-reveal-visible");
        observer.unobserve(entry.target);
      }
    });
  }, { threshold: 0.1 });

  document.querySelectorAll(".scroll-reveal").forEach(function(el) {
    observer.observe(el);
  });
}

/* ── Event Binding ── */

function bindCommonEvents() {
  elements.logoutButton && elements.logoutButton.addEventListener("click", handleLogout);
  elements.loginForm && elements.loginForm.addEventListener("submit", handleLogin);
  elements.userForm && elements.userForm.addEventListener("submit", handleCreateUser);
  elements.workerForm && elements.workerForm.addEventListener("submit", handleCreateWorker);
  elements.jobForm && elements.jobForm.addEventListener("submit", handleCreateJob);
  elements.taskForm && elements.taskForm.addEventListener("submit", handleTaskSubmit);
  elements.configForm && elements.configForm.addEventListener("submit", handleConfigImport);
  elements.configFile && elements.configFile.addEventListener("change", handleConfigFileSelect);
  elements.configKind && elements.configKind.addEventListener("change", refreshConfigsPage);
  elements.configRefresh && elements.configRefresh.addEventListener("click", refreshConfigsPage);
  elements.configList && elements.configList.addEventListener("click", handleConfigListAction);
  elements.approvalList && elements.approvalList.addEventListener("click", handleApprovalAction);
  elements.themeToggle && elements.themeToggle.addEventListener("click", toggleTheme);
}

/* ── Clock ── */

function startClock() {
  var tick = function() {
    if (elements.clock) {
      elements.clock.textContent = new Date().toLocaleTimeString("zh-CN", { hour12: false });
    }
  };
  tick();
  window.setInterval(tick, 1000);
}

/* ── Auth Handlers ── */

async function handleLogin(event) {
  event.preventDefault();

  var formData = new FormData(elements.loginForm);
  var username = String(formData.get("username") || "").trim();
  var password = String(formData.get("password") || "").trim();

  try {
    var result = await request("/api/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ username: username, password: password }),
    });
    state.token = result.token;
    state.user = result.user;
    window.localStorage.setItem("adp.token", state.token);
    updateSessionState();
    if (elements.loginMessage) {
      elements.loginMessage.textContent = "登录成功。";
    }
    showToast("已登录");

    if (!state.refreshTimer) {
      state.refreshTimer = window.setInterval(refreshCurrentPage, 15000);
    }

    if (page === "login") {
      window.location.href = "/";
      return;
    }

    await refreshCurrentPage();
  } catch (error) {
    if (elements.loginMessage) {
      elements.loginMessage.textContent = error.message;
    }
    showToast(error.message);
  }
}

function handleLogout() {
  state.token = "";
  state.user = null;
  window.localStorage.removeItem("adp.token");
  if (state.refreshTimer) {
    window.clearInterval(state.refreshTimer);
    state.refreshTimer = null;
  }
  updateSessionState();
  renderLoggedOutPlaceholders();
  if (elements.loginMessage) {
    elements.loginMessage.textContent = "已退出登录。";
  }
  showToast("已退出登录");
}

/* ── CRUD Handlers ── */

async function handleCreateUser(event) {
  event.preventDefault();
  if (!ensureAuthed()) return;

  try {
    var user = await authedRequest("/api/v1/users", {
      method: "POST",
      body: JSON.stringify({
        username: valueOf("new-username"),
        password: valueOf("new-password"),
        role: valueOf("new-role"),
      }),
    });
    elements.userForm.reset();
    byId("new-role").value = "operator";
    showToast("用户 " + user.username + " 已创建");
    await refreshUsersPage();
  } catch (error) {
    showToast(error.message);
  }
}

/* ── Deploy Mode Toggle ── */
var deployMode = "local";

function switchDeployMode(mode) {
  deployMode = mode;
  var localBtn = document.getElementById("deploy-mode-local");
  var remoteBtn = document.getElementById("deploy-mode-remote");
  var remoteFields = document.getElementById("remote-deploy-fields");
  var submitBtn = document.getElementById("worker-submit-btn");

  if (mode === "remote") {
    localBtn.className = "btn btn-xs btn-ghost";
    remoteBtn.className = "btn btn-xs btn-primary";
    if (remoteFields) remoteFields.style.display = "";
    if (submitBtn) submitBtn.textContent = "部署 Worker";
  } else {
    localBtn.className = "btn btn-xs btn-primary";
    remoteBtn.className = "btn btn-xs btn-ghost";
    if (remoteFields) remoteFields.style.display = "none";
    if (submitBtn) submitBtn.textContent = "创建 Worker";
  }
}

async function handleCreateWorker(event) {
  event.preventDefault();
  if (!ensureAuthed()) return;

  try {
    var body = {
      name: valueOf("worker-name"),
      worker_type: valueOf("worker-type"),
    };

    if (deployMode === "remote") {
      var sshPort = parseInt(valueOf("ssh-port")) || 22;
      body.ssh_host = valueOf("ssh-host");
      body.ssh_port = sshPort;
      body.ssh_user = valueOf("ssh-user");
      body.ssh_key_file = valueOf("ssh-key-file");
      body.log_to_db = document.getElementById("worker-log-to-db").checked;
    }

    var workerResp = await authedRequest("/api/v1/workers", {
      method: "POST",
      body: JSON.stringify(body),
    });

    elements.workerForm.reset();
    byId("worker-type").value = "shell";
    if (deployMode === "remote") {
      switchDeployMode("local");
    }

    if (workerResp.deploying) {
      showToast("Worker " + workerResp.worker.name + " 正在部署到 " + workerResp.ssh_host + " ...");
    } else {
      showToast("Worker " + workerResp.name + " 已创建");
    }
    await refreshWorkersPage();
  } catch (error) {
    showToast(error.message);
  }
}

async function loadWorkerOptions() {
  var select = document.getElementById("job-worker-ids");
  if (!select || !state.token) return;
  try {
    var workers = await authedRequest("/api/v1/workers");
    select.innerHTML = "";
    for (var i = 0; i < workers.length; i++) {
      var w = workers[i];
      var opt = document.createElement("option");
      opt.value = w.id;
      opt.textContent = w.name + " (" + w.worker_type + ") - " + w.status;
      select.appendChild(opt);
    }
  } catch (_) {}
}

async function handleCreateJob(event) {
  event.preventDefault();
  if (!ensureAuthed()) return;

  try {
    var body = {
      name: valueOf("job-name"),
      worker_type: valueOf("job-worker-type"),
      command: valueOf("job-command"),
    };

    var select = document.getElementById("job-worker-ids");
    var selected = [];
    if (select) {
      for (var i = 0; i < select.options.length; i++) {
        if (select.options[i].selected) selected.push(select.options[i].value);
      }
    }
    if (selected.length === 0) {
      showToast("请选择至少一个 Worker");
      return;
    }
    body.worker_ids = selected;

    var result = await authedRequest("/api/v1/jobs", {
      method: "POST",
      body: JSON.stringify(body),
    });

    elements.jobForm.reset();
    byId("job-worker-type").value = "shell";
    await loadWorkerOptions();

    if (result.jobs) {
      showToast("已创建 " + result.total + " 个 Job，分配给 " + (result.worker_ids || []).length + " 个 Worker");
    } else {
      showToast("Job " + result.id + " 已创建");
    }
    await refreshJobsPage();
  } catch (error) {
    showToast(error.message);
  }
}

/* ── YAML Batch Job ── */

var yamlJobForm = document.getElementById("yaml-job-form");
if (yamlJobForm) {
  yamlJobForm.addEventListener("submit", handleYAMLJobSubmit);
}

async function handleYAMLJobSubmit(event) {
  event.preventDefault();
  if (!ensureAuthed()) return;

  var yamlText = document.getElementById("yaml-input").value.trim();
  if (!yamlText) { showToast("请输入 YAML 配置"); return; }

  try {
    var result = await authedRequest("/api/v1/jobs/yaml", {
      method: "POST",
      body: JSON.stringify({ yaml: yamlText }),
    });
    showToast("YAML 批量调度完成：创建了 " + result.total + " 个 Job");
    await refreshJobsPage();
  } catch (error) {
    showToast(error.message);
  }
}

/* ── 点击模板自动填充 Task ── */

// Template code → natural language prompt mapping.
var templatePrompts = {
  mysql_backup:          { prompt: "每天备份 mysql 数据库", params: '{"Database":"demo","ServiceProfile":"mysql_prod"}' },
  http_health_check:     { prompt: "检查 http://127.0.0.1 是否正常",  params: '{"URL":"http://127.0.0.1"}' },
  check_process:         { prompt: "检查 nginx 进程是否运行",       params: '{"Process":"nginx"}' },
  check_port:            { prompt: "检查 80 端口是否监听",          params: '{"Port":"80"}' },
  read_log_tail:         { prompt: "查看 nginx 错误日志",          params: '{"LogFile":"/var/log/nginx/error.log","Lines":"50"}' },
  redis_ping:            { prompt: "检查 Redis 是否响应 PING",     params: '{"Host":"127.0.0.1"}' },
  redis_info:            { prompt: "查看 Redis 内存信息",          params: '{"Host":"127.0.0.1"}' },
  redis_slowlog_get:     { prompt: "查看 Redis 慢查询日志",        params: '{"Host":"127.0.0.1","Count":"10"}' },
  redis_client_list:     { prompt: "查看 Redis 客户端连接列表",    params: '{"Host":"127.0.0.1"}' },
};

function quickFillFromTemplate(code, name, paramsJSON) {
  if (!elements.taskInput) return;

  var preset = templatePrompts[code];
  if (preset) {
    elements.taskInput.value = preset.prompt;
    if (elements.taskParams) elements.taskParams.value = preset.params;
  } else {
    elements.taskInput.value = name;
    // Auto-generate params from template parameter definitions.
    var params = [];
    try {
      params = JSON.parse(decodeURIComponent(paramsJSON));
    } catch (_) {}
    var defaultParams = {};
    for (var i = 0; i < params.length; i++) {
      if (params[i].default) defaultParams[params[i].name] = params[i].default;
    }
    if (elements.taskParams) elements.taskParams.value = JSON.stringify(defaultParams);
  }

  showToast("已选中模板: " + name + " — 点击 解析 Task 查看结果");
}

/* ── Generate YAML from NL ── */

async function handleGenerateYAML() {
  if (!ensureAuthed()) return;
  var input = elements.taskInput ? elements.taskInput.value.trim() : "";
  if (!input) { showToast("先输入任务描述"); return; }
  try {
    var result = await authedRequest("/api/v1/tasks/generate-yaml", {
      method: "POST",
      body: JSON.stringify({ input: input }),
    });
    if (elements.taskOutput) {
      elements.taskOutput.textContent = result.yaml;
      // Store generated YAML for potential save.
      elements.taskOutput.dataset.yamlContent = result.yaml;
      elements.taskOutput.dataset.yamlName = result.parsed_name || "untitled";
    }
    showToast("YAML 已生成" + (result.used_ai ? " (AI)" : " (规则)") + " — 可保存或直接运行");
    // Show save button.
    var saveBtn = document.getElementById("save-yaml-btn");
    if (saveBtn) saveBtn.style.display = "";
  } catch (error) {
    showToast(error.message);
  }
}

async function handleSaveGeneratedYAML() {
  if (!ensureAuthed()) return;
  var yamlContent = elements.taskOutput ? elements.taskOutput.dataset.yamlContent : "";
  if (!yamlContent) { showToast("先生成 YAML"); return; }
  var name = elements.taskOutput.dataset.yamlName || "untitled";
  try {
    await authedRequest("/api/v1/yamls", {
      method: "POST",
      body: JSON.stringify({
        name: name,
        description: elements.taskInput ? elements.taskInput.value.trim() : "",
        yaml_content: yamlContent,
      }),
    });
    showToast("YAML 已保存: " + name);
    await refreshYAMLList();
  } catch (error) {
    showToast(error.message);
  }
}

async function handleRunStoredYAML(yamlId) {
  if (!ensureAuthed()) return;
  try {
    var result = await authedRequest("/api/v1/yamls/" + yamlId + "/run", { method: "POST" });
    showToast("已从 YAML 创建 " + result.total + " 个 Job");
    await refreshTasksPage();
  } catch (error) {
    showToast(error.message);
  }
}

async function handleDeleteStoredYAML(yamlId) {
  if (!ensureAuthed()) return;
  if (!confirm("确定删除此 YAML 模板？")) return;
  try {
    await authedRequest("/api/v1/yamls/" + yamlId, { method: "DELETE" });
    showToast("YAML 已删除");
    await refreshYAMLList();
  } catch (error) {
    showToast(error.message);
  }
}

async function refreshYAMLList() {
  var container = document.getElementById("yaml-list");
  if (!container || !state.token) return;
  try {
    var yamls = await authedRequest("/api/v1/yamls");
    renderList(container, yamls, function(jy) {
      return '<div class="list-card">' +
        '<div style="flex: 1;">' +
          '<strong style="font-size: 0.875rem;">' + escapeHTML(jy.name) + '</strong>' +
          '<span style="font-size: 0.6875rem; color: var(--text-tertiary); margin-left: 8px;">' + escapeHTML(jy.source) + '</span>' +
        '</div>' +
        '<div class="list-card-meta">' +
          '<span class="mono" style="font-size: 0.6875rem;">' + escapeHTML(jy.id) + '</span>' +
          '<span>' + formatTime(jy.created_at) + '</span>' +
        '</div>' +
        '<div style="display: flex; gap: 4px; margin-left: 8px;">' +
          '<button class="btn btn-xs btn-primary" onclick="handleRunStoredYAML(\'' + escapeHTML(jy.id) + '\')">运行</button>' +
          '<button class="btn btn-xs btn-ghost" onclick="handleDeleteStoredYAML(\'' + escapeHTML(jy.id) + '\')" style="color: var(--danger);">删除</button>' +
        '</div>' +
      '</div>';
    }, "暂无保存的 YAML 模板。");
  } catch (_) {}
}

async function handleTaskSubmit(event) {
  event.preventDefault();
  if (!ensureAuthed()) return;

  var action = (event.submitter && event.submitter.dataset.action) || "parse";
  var input = elements.taskInput ? elements.taskInput.value.trim() : "";
  if (!input) {
    showToast("先输入任务描述");
    return;
  }

  var parameters;
  try {
    parameters = parseOptionalJSON((elements.taskParams ? elements.taskParams.value.trim() : "") || "");
  } catch (error) {
    showToast(error.message);
    return;
  }

  try {
    if (action === "parse") {
      var parseResult = await authedRequest("/api/v1/tasks/parse", {
        method: "POST",
        body: JSON.stringify({ input: input }),
      });
      if (elements.taskOutput) {
        elements.taskOutput.textContent = JSON.stringify(parseResult, null, 2);
      }
      if (parseResult.intent === "diagnose") {
        showToast("已识别为诊断意图，多步骤计划请点击生成 YAML");
        return;
      }
      showToast("Task 解析完成");
      return;
    }

    var runResult = await authedRequest("/api/v1/tasks/run", {
      method: "POST",
      body: JSON.stringify({ input: input, parameters: parameters }),
    });
    if (elements.taskOutput) {
      elements.taskOutput.textContent = JSON.stringify(runResult, null, 2);
    }
    showToast(runResult.approval_required ? "Task 已进入审批队列" : "Task 已创建");
    await refreshTasksPage();
  } catch (error) {
    if (elements.taskOutput) {
      elements.taskOutput.textContent = error.message;
    }
    showToast(error.message);
  }
}

async function handleApprovalAction(event) {
  var button = event.target.closest("[data-approval-id]");
  if (!button) return;
  if (!ensureAuthed()) return;

  var approved = button.dataset.decision === "approve";
  try {
    var result = await authedRequest("/api/v1/approvals/jobs/" + button.dataset.approvalId, {
      method: "POST",
      body: JSON.stringify({
        approved: approved,
        comment: approved ? "Approved from UI" : "Rejected from UI",
      }),
    });
    showToast(approved ? "已批准 " + result.id : "已拒绝 " + result.id);
    await refreshCurrentPage();
  } catch (error) {
    showToast(error.message);
  }
}


/* ── Page Refresh ── */
async function refreshCurrentPage() {
  if (!state.token) {
    renderLoggedOutPlaceholders();
    return;
  }

  try {
    switch (page) {
      case "home":
        await refreshHomePage();
        break;
      case "users":
        await refreshUsersPage();
        break;
      case "workers":
        await refreshWorkersPage();
        break;
      case "jobs":
        await refreshJobsPage();
        break;
      case "tasks":
        await refreshTasksPage();
        break;
      case "configs":
        await refreshConfigsPage();
        break;
      default:
        await refreshSessionOnly();
        break;
    }
  } catch (error) {
    if (error.code === 401) {
      handleLogout();
      return;
    }
    showToast(error.message);
  }
}

async function refreshSessionOnly() {
  var summary = await authedRequest("/api/v1/dashboard/summary");
  state.user = summary.user;
  updateSessionState(summary.current_time);
}

async function refreshHomePage() {
  var summary = await authedRequest("/api/v1/dashboard/summary");
  state.user = summary.user;
  updateSessionState(summary.current_time);
  renderSummaryMetrics(summary);
  renderApprovals(summary.pending_approvals);
  renderAuditLogs(summary.recent_audit_logs);
}

async function refreshUsersPage() {
  var summary = await authedRequest("/api/v1/dashboard/summary");
  state.user = summary.user;
  updateSessionState(summary.current_time);

  if (state.user.role !== "admin") {
    if (elements.usersAccessNote) {
      elements.usersAccessNote.textContent = "当前账户不是管理员，无法查看或创建用户。";
    }
    renderList(elements.userList, [], function() { return ""; }, "请使用管理员账户登录。");
    return;
  }

  if (elements.usersAccessNote) {
    elements.usersAccessNote.textContent = "当前为管理员，可创建与查看用户。";
  }

  var users = await authedRequest("/api/v1/users");
  renderList(
    elements.userList,
    users,
    function(user) {
      var isAdmin = state.user && state.user.role === "admin";
      var buttons = '';
      if (isAdmin && user.username !== state.user.username) {
        buttons += '<button class="btn btn-xs btn-ghost" onclick="handleDeleteUser(\'' + escapeHTML(user.username) + '\')" style="color: var(--danger);">删除</button>';
      }
      if (isAdmin || (state.user && state.user.username === user.username)) {
        buttons += '<button class="btn btn-xs btn-ghost" onclick="handleChangePassword(\'' + escapeHTML(user.username) + '\')">改密</button>';
      }
      return '<div class="list-card">' +
        '<div style="flex: 1;">' +
          '<strong style="font-size: 0.875rem;">' + escapeHTML(user.username) + '</strong>' +
          '<span style="font-size: 0.75rem; color: var(--text-secondary); margin-left: 10px;">' + escapeHTML(user.role) + '</span>' +
        '</div>' +
        '<div style="display: flex; gap: 6px; align-items: center;">' +
          '<span class="status-pill" style="background: var(--info-bg); color: var(--info);">' + escapeHTML(user.role) + '</span>' +
          buttons +
        '</div>' +
      '</div>';
    },
    "暂无用户。"
  );
}

async function refreshWorkersPage() {
  var summary = await authedRequest("/api/v1/dashboard/summary");
  state.user = summary.user;
  updateSessionState(summary.current_time);

  var workers = await authedRequest("/api/v1/workers");
  renderList(
    elements.workerList,
    workers,
    function(worker) {
      var hostInfo = '';
      if (worker.host_info && worker.host_info.hostname) {
        hostInfo = '<div style="font-size: 0.7rem; color: var(--text-tertiary); margin-top: 2px;">' +
          escapeHTML(worker.host_info.hostname) + ' | ' + escapeHTML(worker.host_info.ip_address || '--') +
          ' | CPU:' + (worker.host_info.cpu_usage || 0).toFixed(0) + '%' +
          ' | Disk:' + (worker.host_info.storage_usage || 0).toFixed(0) + '%' +
        '</div>';
      }
      var buttons = '';
      if (state.user) {
        buttons += '<button class="btn btn-xs btn-ghost" onclick="handleStopWorker(\'' + escapeHTML(worker.id) + '\', false)" style="color: var(--warning);">停止</button>';
        buttons += '<button class="btn btn-xs btn-ghost" onclick="handleRestartWorker(\'' + escapeHTML(worker.id) + '\')">重启</button>';
        buttons += '<button class="btn btn-xs btn-ghost" onclick="handleDeleteWorkerById(\'' + escapeHTML(worker.id) + '\')" style="color: var(--danger);">删除</button>';
      }
      return '<div class="list-card">' +
        '<div style="flex: 1;">' +
          '<strong style="font-size: 0.875rem;">' + escapeHTML(worker.name) + '</strong>' +
          '<span class="mono" style="font-size: 0.6875rem; color: var(--text-tertiary); margin-left: 8px;">' + escapeHTML(worker.id) + '</span>' +
          hostInfo +
        '</div>' +
        '<div class="list-card-meta">' +
          '<span class="status-pill ' + statusClass(worker.status) + '"><span class="status-dot"></span>' + escapeHTML(worker.status) + '</span>' +
          '<span>' + formatTime(worker.last_heartbeat_at) + '</span>' +
        '</div>' +
        (buttons ? '<div style="display: flex; gap: 4px; margin-left: 8px;">' + buttons + '</div>' : '') +
      '</div>';
    },
    "暂无 Worker。"
  );
}

async function refreshJobsPage() {
  var summary = await authedRequest("/api/v1/dashboard/summary");
  state.user = summary.user;
  updateSessionState(summary.current_time);

  var jobs = await authedRequest("/api/v1/jobs?limit=16");
  await loadWorkerOptions();
  renderList(
    elements.jobList,
    jobs,
    function(job) {
      var deletable = ["pending", "queued", "waiting_approval"].indexOf(job.status) >= 0;
      var deleteBtn = '';
      var dispatchBtn = '';
      if (job.status === "pending" && state.user) {
        dispatchBtn = '<button class="btn btn-xs btn-primary" onclick="handleDispatchJob(\'' + escapeHTML(job.id) + '\')">调度</button>';
      }
      if (deletable && state.user) {
        deleteBtn = '<button class="btn btn-xs btn-ghost" onclick="handleDeleteJob(\'' + escapeHTML(job.id) + '\')" style="color: var(--danger);">删除</button>';
      }
      return '<div class="list-card">' +
        '<div style="flex: 1;">' +
          '<strong style="font-size: 0.875rem;">' + escapeHTML(job.name) + '</strong>' +
          '<span style="font-size: 0.75rem; color: var(--text-secondary); margin-left: 8px;">' + escapeHTML(job.command || "无命令详情") + '</span>' +
        '</div>' +
        '<div class="list-card-meta">' +
          '<span class="status-pill ' + statusClass(job.status) + '"><span class="status-dot"></span>' + escapeHTML(job.status) + '</span>' +
          '<span class="mono">' + escapeHTML(job.worker_type) + '</span>' +
          '<span>' + formatTime(job.updated_at) + '</span>' +
        '</div>' +
        ((dispatchBtn || deleteBtn) ? '<div style="display:flex; gap:6px; margin-left: 8px;">' + dispatchBtn + deleteBtn + '</div>' : '') +
      '</div>';
    },
    "暂无 Job。"
  );
  renderApprovals(summary.pending_approvals);
}

async function handleDispatchJob(jobID) {
  if (!ensureAuthed()) return;
  var value = window.prompt("输入目标 Worker ID，多个用英文逗号分隔");
  if (!value) return;
  var workerIDs = value.split(",").map(function(item) {
    return item.trim();
  }).filter(Boolean);
  if (workerIDs.length === 0) {
    showToast("请输入 Worker ID");
    return;
  }
  try {
    var result = await authedRequest("/api/v1/jobs/" + encodeURIComponent(jobID) + "/dispatch", {
      method: "POST",
      body: JSON.stringify({ worker_ids: workerIDs }),
    });
    showToast("已调度 " + result.total + " 个 Job");
    await refreshJobsPage();
  } catch (error) {
    showToast(error.message);
  }
}

async function refreshTasksPage() {
  var summary = await authedRequest("/api/v1/dashboard/summary");
  state.user = summary.user;
  updateSessionState(summary.current_time);

  var results = await Promise.all([
    authedRequest("/api/v1/tasks"),
    authedRequest("/api/v1/templates"),
  ]);
  var tasks = results[0];
  var templates = results[1];

  renderList(
    elements.taskList,
    tasks,
    function(task) {
      return '<div class="list-card">' +
        '<div style="flex: 1;">' +
          '<strong style="font-size: 0.875rem;">' + escapeHTML(task.name) + '</strong>' +
          '<span style="font-size: 0.75rem; color: var(--text-secondary); margin-left: 8px;">' + escapeHTML(task.command || "无命令详情") + '</span>' +
        '</div>' +
        '<div class="list-card-meta">' +
          '<span class="status-pill ' + statusClass(task.status) + '"><span class="status-dot"></span>' + escapeHTML(task.status) + '</span>' +
          '<span class="mono">' + escapeHTML(task.template_code || "--") + '</span>' +
          '<span>' + formatTime(task.created_at) + '</span>' +
        '</div>' +
      '</div>';
    },
    "暂无 Task 记录。"
  );

  renderList(
    elements.templateList,
    templates,
    function(template) {
      return '<div class="list-card template-card" onclick="quickFillFromTemplate(\'' + escapeHTML(template.code) + '\',\'' + escapeHTML(template.name) + '\',\'' + escapeHTML(JSON.stringify(template.parameters || [])) + '\')" style="cursor: pointer;">' +
        '<div style="flex: 1;">' +
          '<strong style="font-size: 0.875rem;">' + escapeHTML(template.name) + '</strong>' +
          '<span style="font-size: 0.75rem; color: var(--text-secondary); margin-left: 8px;">' + escapeHTML(template.description || template.code) + '</span>' +
        '</div>' +
        '<div class="list-card-meta">' +
          '<span class="status-pill" style="background: var(--accent-bg); color: var(--accent);">' + escapeHTML(template.tool_type) + '</span>' +
          '<span class="mono">' + escapeHTML(template.code) + '</span>' +
          '<span style="font-size: 0.6875rem; color: var(--accent);">点击使用 →</span>' +
        '</div>' +
      '</div>';
    },
    "暂无模板。"
  );
  await refreshYAMLList();
}

/* ── Managed Configs ── */

function handleConfigFileSelect(event) {
  var file = event.target.files && event.target.files[0];
  if (!file || !elements.configYAML) return;
  var reader = new FileReader();
  reader.onload = function() {
    elements.configYAML.value = String(reader.result || "");
    showToast("已读取 " + file.name);
  };
  reader.onerror = function() { showToast("读取文件失败"); };
  reader.readAsText(file, "UTF-8");
}

async function handleConfigImport(event) {
  event.preventDefault();
  if (!ensureAuthed()) return;
  var kind = elements.configKind ? elements.configKind.value : "";
  var yamlContent = elements.configYAML ? elements.configYAML.value.trim() : "";
  if (!kind || !yamlContent) { showToast("请选择类型并提供 YAML 内容"); return; }
  try {
    var result = await authedRequest("/api/v1/configs/" + encodeURIComponent(kind), {
      method: "POST", body: JSON.stringify({ yaml_content: yamlContent }),
    });
    showToast("配置已导入并生效：" + (result.name || result.id));
    await refreshConfigsPage();
  } catch (error) { showToast(error.message); }
}

async function refreshConfigsPage() {
  if (!state.token || !elements.configList || !elements.configKind) return;
  var kind = elements.configKind.value;
  var configs = await authedRequest("/api/v1/configs/" + encodeURIComponent(kind));
  renderList(elements.configList, configs, function(cfg) {
    return '<div class="list-card"><div style="flex:1;min-width:0;">' +
      '<strong style="font-size:.875rem;">' + escapeHTML(cfg.name || cfg.id) + '</strong>' +
      '<span class="mono" style="margin-left:8px;font-size:.75rem;">' + escapeHTML(cfg.id) + '</span>' +
      '<pre class="code-block" style="margin:10px 0 0;max-height:130px;overflow:auto;">' + escapeHTML(cfg.yaml_content || "") + '</pre>' +
      '</div><button class="btn btn-xs btn-ghost" type="button" data-config-delete="' + escapeHTML(cfg.id) + '">删除</button></div>';
  }, "当前类型暂无已导入配置。");
}

async function handleConfigListAction(event) {
  var button = event.target.closest("[data-config-delete]");
  if (!button || !elements.configKind || !ensureAuthed()) return;
  var id = button.dataset.configDelete;
  if (!confirm("确定删除配置 " + id + " 吗？")) return;
  try {
    await authedRequest("/api/v1/configs/" + encodeURIComponent(elements.configKind.value) + "/" + encodeURIComponent(id), { method: "DELETE" });
    showToast("配置已删除"); await refreshConfigsPage();
  } catch (error) { showToast(error.message); }
}

/* ── New Feature Handlers ── */

async function handleDeleteUser(username) {
  if (!ensureAuthed()) return;
  if (!confirm("确定要删除用户 " + username + " 吗？")) return;
  try {
    await authedRequest("/api/v1/users/" + username, { method: "DELETE" });
    showToast("用户 " + username + " 已删除");
    await refreshCurrentPage();
  } catch (error) {
    showToast(error.message);
  }
}

async function handleChangePassword(username) {
  if (!ensureAuthed()) return;
  var newPass = prompt("为 " + username + " 输入新密码：");
  if (!newPass) return;
  try {
    await authedRequest("/api/v1/users/" + username + "/password", {
      method: "PUT",
      body: JSON.stringify({ new_password: newPass }),
    });
    showToast("密码已更新");
  } catch (error) {
    showToast(error.message);
  }
}

async function handleDeleteJob(jobId) {
  if (!ensureAuthed()) return;
  if (!confirm("确定要删除 Job " + jobId + " 吗？")) return;
  try {
    await authedRequest("/api/v1/jobs/" + jobId, { method: "DELETE" });
    showToast("Job " + jobId + " 已删除");
    await refreshCurrentPage();
  } catch (error) {
    showToast(error.message);
  }
}

async function handleStopWorker(workerId, force) {
  if (!ensureAuthed()) return;
  try {
    var url = "/api/v1/workers/" + workerId + "/stop";
    if (force) url += "?force=true";
    await authedRequest(url, { method: "POST" });
    showToast(force ? "已发送强制停止指令" : "已发送停止指令");
    await refreshCurrentPage();
  } catch (error) {
    showToast(error.message);
  }
}

async function handleRestartWorker(workerId) {
  if (!ensureAuthed()) return;
  try {
    await authedRequest("/api/v1/workers/" + workerId + "/restart", { method: "POST" });
    showToast("已发送重启指令");
    await refreshCurrentPage();
  } catch (error) {
    showToast(error.message);
  }
}

async function handleDeleteWorkerById(workerId) {
  if (!ensureAuthed()) return;
  if (!confirm("确定要删除 Worker " + workerId + " 吗？此操作不可撤销。")) return;
  try {
    await authedRequest("/api/v1/workers/" + workerId, { method: "DELETE" });
    showToast("Worker " + workerId + " 已删除");
    await refreshCurrentPage();
  } catch (error) {
    showToast(error.message);
  }
}

/* ── Render Helpers ── */

function renderSummaryMetrics(summary) {
  if (!elements.metricsGrid) return;

  var metrics = [
    ["在线 Workers", summary.metrics.workers_online, summary.workers.length + " 个已注册"],
    ["Jobs 总数", summary.metrics.jobs_total, summary.metrics.jobs_success + " 成功 / " + summary.metrics.jobs_failed + " 失败"],
    ["待审批", summary.metrics.jobs_waiting_approval, "等待人工确认"],
    ["模板总数", summary.templates_total, "可用于 Task 解析"],
  ];

  elements.metricsGrid.innerHTML = metrics.map(function(m) {
    return '<div class="metric-card">' +
      '<div class="metric-label">' + escapeHTML(String(m[0])) + '</div>' +
      '<div class="metric-value">' + escapeHTML(String(m[1])) + '</div>' +
      '<div class="metric-desc">' + escapeHTML(String(m[2])) + '</div>' +
    '</div>';
  }).join("");
}

function renderApprovals(items) {
  renderList(
    elements.approvalList,
    items,
    function(job) {
      return '<div class="list-card">' +
        '<div style="flex: 1;">' +
          '<strong style="font-size: 0.875rem;">' + escapeHTML(job.name) + '</strong>' +
          '<span style="font-size: 0.75rem; color: var(--text-secondary); margin-left: 8px;">' + escapeHTML(job.command || "无命令详情") + '</span>' +
        '</div>' +
        '<div class="list-card-meta">' +
          '<span class="status-pill ' + statusClass(job.status) + '"><span class="status-dot"></span>' + escapeHTML(job.status) + '</span>' +
          '<span>' + escapeHTML(job.risk_level || "--") + '</span>' +
          '<span>' + formatTime(job.created_at) + '</span>' +
        '</div>' +
        '<div style="display: flex; gap: 6px; margin-left: 12px;">' +
          '<button class="btn btn-xs btn-primary" type="button" data-approval-id="' + escapeHTML(job.id) + '" data-decision="approve">批准</button>' +
          '<button class="btn btn-xs btn-ghost" type="button" data-approval-id="' + escapeHTML(job.id) + '" data-decision="reject">拒绝</button>' +
        '</div>' +
      '</div>';
    },
    "当前没有待审批任务。"
  );
}

function renderAuditLogs(items) {
  renderList(
    elements.auditList,
    items,
    function(log) {
      return '<div class="list-card">' +
        '<div style="flex: 1;">' +
          '<strong style="font-size: 0.875rem;">' + escapeHTML(log.action) + '</strong>' +
          '<span style="font-size: 0.75rem; color: var(--text-secondary); margin-left: 8px;">' + escapeHTML(log.actor_type + ":" + log.actor_id + " -> " + log.resource_type + ":" + log.resource_id) + '</span>' +
        '</div>' +
        '<div class="list-card-meta">' +
          '<span class="status-pill" style="background: var(--surface-inset); color: var(--text-secondary);">' + escapeHTML(log.resource_type) + '</span>' +
          '<span>' + formatTime(log.created_at) + '</span>' +
        '</div>' +
      '</div>';
    },
    "暂无审计记录。"
  );
}

function renderLoggedOutPlaceholders() {
  if (elements.loginMessage && page === "login") {
    elements.loginMessage.textContent = "登录后即可进入用户、Workers、Jobs、Tasks 页面进行操作。";
  }
  renderList(elements.userList, [], function() { return ""; }, "登录后显示用户列表。");
  renderList(elements.workerList, [], function() { return ""; }, "登录后显示 Worker 列表。");
  renderList(elements.jobList, [], function() { return ""; }, "登录后显示 Job 列表。");
  renderList(elements.taskList, [], function() { return ""; }, "登录后显示 Task 记录。");
  renderList(elements.templateList, [], function() { return ""; }, "登录后显示模板。");
  renderList(elements.approvalList, [], function() { return ""; }, "登录后显示待审批任务。");
  renderList(elements.auditList, [], function() { return ""; }, "登录后显示审计记录。");
  if (elements.metricsGrid) {
    elements.metricsGrid.innerHTML = "";
  }
  if (elements.taskOutput) {
    elements.taskOutput.textContent = "等待输入任务。";
  }
}

function renderList(container, items, renderer, emptyText) {
  if (!container) return;
  if (!items || items.length === 0) {
    container.innerHTML = '<div class="empty-state">' + escapeHTML(emptyText) + '</div>';
    return;
  }
  container.innerHTML = items.map(renderer).join("");
}

function updateSessionState(serverTime) {
  if (!elements.sessionState) return;
  if (state.user && state.user.username) {
    elements.sessionState.textContent = state.user.username + " / " + state.user.role;
    // Show logout button, hide login button
    if (elements.logoutButton) elements.logoutButton.style.display = "";
    var loginBtn = document.getElementById("login-nav-btn");
    if (loginBtn) loginBtn.style.display = "none";
    if (elements.loginMessage && serverTime) {
      elements.loginMessage.textContent = "最近同步：" + formatTime(serverTime);
    }
    return;
  }
  elements.sessionState.textContent = "未登录";
  // Show login button, hide logout button
  if (elements.logoutButton) elements.logoutButton.style.display = "none";
  var loginBtn = document.getElementById("login-nav-btn");
  if (loginBtn) loginBtn.style.display = "";
}

/* ── Auth Helpers ── */

function ensureAuthed() {
  if (state.token) return true;
  showToast("请先登录");
  if (page !== "login") {
    window.location.href = "/login";
  }
  return false;
}

async function authedRequest(url, options) {
  options = options || {};
  options.headers = options.headers || {};
  options.headers.Authorization = "Bearer " + state.token;
  return request(url, options);
}

async function request(url, options) {
  options = options || {};
  var response = await window.fetch(url, {
    method: options.method || "GET",
    headers: Object.assign({ "Content-Type": "application/json" }, options.headers || {}),
    body: options.body,
  });

  var contentType = response.headers.get("content-type") || "";
  var payload = contentType.includes("application/json")
    ? await response.json()
    : await response.text();

  if (!response.ok) {
    var message = typeof payload === "string" ? payload : (payload.error || "请求失败");
    var error = new Error(message);
    error.code = response.status;
    throw error;
  }

  return payload;
}

/* ── Utilities ── */

function valueOf(id) {
  var el = byId(id);
  return el ? String(el.value || "").trim() : "";
}

function parseOptionalJSON(raw) {
  if (!raw) return undefined;
  try {
    var parsed = JSON.parse(raw);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed;
    }
    throw new Error("参数 JSON 必须是对象");
  } catch (_error) {
    throw new Error("参数 JSON 格式不正确");
  }
}

function formatTime(value) {
  if (!value) return "--";
  var date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return date.toLocaleString("zh-CN", {
    hour12: false,
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function statusClass(status) {
  return "is-" + String(status || "").toLowerCase();
}

function showToast(message) {
  if (!elements.toast) return;
  window.clearTimeout(state.toastTimer);
  elements.toast.hidden = false;
  elements.toast.textContent = message;
  elements.toast.classList.add("is-visible");
  state.toastTimer = window.setTimeout(function() {
    elements.toast.classList.remove("is-visible");
    elements.toast.hidden = true;
  }, 2400);
}

function escapeHTML(value) {
  return String(value)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

function byId(id) {
  return document.getElementById(id);
}

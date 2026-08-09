const state = {
  token: window.localStorage.getItem("adp.token") || "",
  user: null,
  toastTimer: null,
  refreshTimer: null,
  refreshInFlight: false,
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
  agentTimeline: byId("agent-timeline"),
  templateList: byId("template-list"),
  configForm: byId("config-form"),
  configKind: byId("config-kind"),
  configFile: byId("config-file"),
  configYAML: byId("config-yaml"),
  configList: byId("config-list"),
  configRefresh: byId("config-refresh"),
  configsAccessNote: byId("configs-access-note"),
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
    startJobsAutoRefresh();
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
  var conversationMessages = document.getElementById("conversation-messages");
  conversationMessages && conversationMessages.addEventListener("click", handleConversationAction);
  elements.themeToggle && elements.themeToggle.addEventListener("click", toggleTheme);
  var templateSel = document.getElementById("job-template");
  if (templateSel) templateSel.addEventListener("change", onTemplateChange);
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

    if (page === "login") {
      window.location.href = "/";
      return;
    }

    await refreshCurrentPage();
    startJobsAutoRefresh();
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
  state.refreshInFlight = false;
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

var templateCache = [];

async function loadTemplateOptions(selectEl) {
  if (!selectEl || !state.token) return;
  try {
    var caps = await authedRequest("/api/v1/dashboard/summary");
    templateCache = caps.templates || [];
    selectEl.innerHTML = '<option value="">选择 YAML 模板…</option>';
    for (var i = 0; i < templateCache.length; i++) {
      var t = templateCache[i];
      var opt = document.createElement("option");
      opt.value = t.code;
      opt.dataset.params = JSON.stringify(t.parameters || []);
      opt.dataset.command = t.command || "";
      opt.dataset.workerType = t.worker_type || "shell";
      opt.dataset.risk = t.risk_level || "low";
      opt.textContent = t.name + " (" + t.code + ") [" + (t.risk_level || "low") + "]";
      selectEl.appendChild(opt);
    }
  } catch (_) {}
}

async function loadWorkerDropdown(selectEl) {
  if (!selectEl || !state.token) return;
  try {
    var workers = await authedRequest("/api/v1/workers");
    selectEl.innerHTML = '<option value="">选择 Worker…</option>';
    for (var i = 0; i < workers.length; i++) {
      var w = workers[i];
      var opt = document.createElement("option");
      opt.value = w.id;
      opt.dataset.workerType = w.worker_type || "shell";
      opt.textContent = w.name + " (" + w.worker_type + ") - " + w.status;
      selectEl.appendChild(opt);
    }
  } catch (_) {}
}

function onTemplateChange() {
  var sel = document.getElementById("job-template");
  var container = document.getElementById("job-params-container");
  if (!sel || !container) return;
  var opt = sel.options[sel.selectedIndex];
  if (!opt || !opt.dataset.params) { container.innerHTML = ""; return; }
  try {
    var params = JSON.parse(opt.dataset.params);
    var html = "";
    for (var i = 0; i < params.length; i++) {
      var p = params[i];
      html += '<div class="field-group"><label class="field-label">' + escapeHTML(p.name) +
        (p.required ? ' <span style="color:var(--accent);">*</span>' : '') +
        '</label><input class="field-input job-param-input" data-param-name="' + escapeHTML(p.name) +
        '" type="text" placeholder="' + escapeHTML(p.description || "") +
        '" value="' + escapeHTML(p.default || "") + '"></div>';
    }
    container.innerHTML = html;
  } catch (_) { container.innerHTML = ""; }
}

function renderTemplateCommand(command, params) {
  var result = command;
  for (var key in params) {
    if (params.hasOwnProperty(key)) {
      result = result.split("{{." + key + "}}").join(params[key]);
      result = result.split("{{." + key + " }}").join(params[key]);
    }
  }
  return result;
}

var jobMode = "template";

function switchJobMode(mode) {
  jobMode = mode;
  var templateBtn = document.getElementById("mode-template-btn");
  var shellBtn = document.getElementById("mode-shell-btn");
  var templateSection = document.getElementById("template-section");
  var cmdTextarea = document.getElementById("job-command");
  var typeGroup = document.getElementById("job-worker-type-group");
  if (templateBtn) templateBtn.className = mode === "template" ? "btn btn-xs btn-primary" : "btn btn-xs btn-ghost";
  if (shellBtn) shellBtn.className = mode === "shell" ? "btn btn-xs btn-primary" : "btn btn-xs btn-ghost";
  if (templateSection) templateSection.style.display = mode === "shell" ? "none" : "";
  if (cmdTextarea) cmdTextarea.style.display = mode === "template" ? "none" : "";
  if (typeGroup) typeGroup.style.display = mode === "shell" ? "" : "none";
}

async function handleCreateJob(event) {
  event.preventDefault();
  if (!ensureAuthed()) return;

  var workerSel = document.getElementById("job-worker");
  var nameInput = document.getElementById("job-name");
  if (!workerSel || !workerSel.value) { showToast("请选择 Worker"); return; }
  if (!nameInput || !nameInput.value.trim()) { showToast("请输入任务名"); return; }

  var workerID = workerSel.value;
  var body = { name: nameInput.value.trim(), worker_ids: [workerID] };

  if (jobMode === "template") {
    // Template mode: use selected template or fall back to direct command
    var templateSel = document.getElementById("job-template");
    var templateOpt = templateSel && templateSel.options[templateSel.selectedIndex];
    var cmdTextarea = document.getElementById("job-command");

    if (templateOpt && templateOpt.value) {
      // Template selected — render params
      var workerType = templateOpt.dataset.workerType || "shell";
      var commandTemplate = templateOpt.dataset.command || "";
      var params = {};
      var paramInputs = document.querySelectorAll(".job-param-input");
      for (var i = 0; i < paramInputs.length; i++) {
        var inp = paramInputs[i];
        if (inp.value.trim()) params[inp.dataset.paramName] = inp.value.trim();
      }
      var paramDefs = [];
      try { paramDefs = JSON.parse(templateOpt.dataset.params || "[]"); } catch (_) {}
      for (var j = 0; j < paramDefs.length; j++) {
        if (paramDefs[j].required && !params[paramDefs[j].name]) { showToast("缺少必填参数: " + paramDefs[j].name); return; }
      }
      body.worker_type = workerType;
      body.command = renderTemplateCommand(commandTemplate, params);
      body.template_code = templateOpt.value;
      body.parameters = params;
    } else {
      // No template selected — use directly typed command
      var cmd = cmdTextarea ? cmdTextarea.value.trim() : "";
      if (!cmd) { showToast("请选择模板或输入命令"); return; }
      body.worker_type = "shell";
      body.command = cmd;
    }
  } else {
    // Shell mode: direct command
    var cmd = (document.getElementById("job-command") || {}).value;
    if (!cmd || !cmd.trim()) { showToast("请输入命令"); return; }
    var wtype = (document.getElementById("job-worker-type-inp") || {}).value || "shell";
    body.worker_type = wtype;
    body.command = cmd.trim();
  }

  try {
    var result = await authedRequest("/api/v1/jobs", { method: "POST", body: JSON.stringify(body) });
    showToast("Job " + (result.jobs ? "已批量创建" : result.id + " 已创建"));
    workerSel.value = "";
    var ts = document.getElementById("job-template"); if (ts) ts.value = "";
    document.getElementById("job-params-container").innerHTML = "";
    if (nameInput) nameInput.value = "";
    var ct = document.getElementById("job-command"); if (ct) ct.value = "";
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

var currentConversationID = "";

async function handleTaskSubmit(event) {
  event.preventDefault();
  if (!ensureAuthed()) return;

  var input = elements.taskInput ? elements.taskInput.value.trim() : "";
  if (!input) { showToast("先输入任务描述"); return; }

  // Immediately render user message.
  var msgEl = document.getElementById("conversation-messages");
  var userBubble = null;
  if (msgEl) {
    userBubble = document.createElement("div");
    userBubble.style.cssText = "display:flex;justify-content:flex-end;margin:8px 0;";
    userBubble.innerHTML = '<div style="max-width:75%;background:var(--accent);color:#fff;padding:8px 14px;border-radius:16px 16px 4px 16px;font-size:.8125rem;line-height:1.55;white-space:pre-wrap;word-break:break-word;">' + escapeHTML(input) + '</div>';
    msgEl.appendChild(userBubble);
    msgEl.scrollTop = msgEl.scrollHeight;
  }
  if (elements.taskInput) elements.taskInput.value = "";

  // Create Agent bubble for streaming.
  var agentBubble = null;
  var agentContent = null;
  var toolList = null;
  var finalAnswer = "";
  if (msgEl) {
    agentBubble = document.createElement("div");
    agentBubble.style.cssText = "display:flex;justify-content:flex-start;margin:8px 0;";
    agentContent = document.createElement("div");
    agentContent.style.cssText = "max-width:85%;background:var(--surface-inset);border:1px solid var(--border);padding:10px 14px;border-radius:16px 16px 16px 4px;font-size:.8125rem;line-height:1.6;min-width:60px;";
    agentContent.innerHTML = '<span style="color:var(--text-tertiary);">思考中…</span>';
    agentBubble.appendChild(agentContent);
    msgEl.appendChild(agentBubble);
    msgEl.scrollTop = msgEl.scrollHeight;
  }

<<<<<<< HEAD
  var indicator = document.getElementById("agent-running-indicator");
  if (indicator) indicator.style.display = "flex";

  try {
    var body = { input: input, stream: true };
    if (currentConversationID) body.conversation_id = currentConversationID;

    var resp = await fetch("/api/v1/agent/runs", {
      method: "POST",
      headers: { "Content-Type": "application/json", "Authorization": "Bearer " + state.token },
      body: JSON.stringify(body),
    });

    if (!resp.ok) {
      var errText = await resp.text();
      throw new Error("Agent API error: " + resp.status + " " + errText);
    }

    var reader = resp.body.getReader();
    var decoder = new TextDecoder();
    var buf = "";
    var pendingApprovals = null;

    while (true) {
      var chunk = await reader.read();
      if (chunk.done) break;
      buf += decoder.decode(chunk.value, {stream: true});
      var lines = buf.split("\n");
      buf = lines.pop() || "";
      for (var l = 0; l < lines.length; l++) {
        var line = lines[l].trim();
        if (!line.startsWith("data: ")) continue;
        try {
          var ev = JSON.parse(line.slice(6));
          if (ev.type === "tool") {
            // Add collapsed tool entry.
            if (!toolList) {
              toolList = document.createElement("div");
              toolList.style.cssText = "margin-bottom:4px;";
              if (agentContent) agentContent.insertBefore(toolList, agentContent.firstChild);
            }
            var toolDiv = document.createElement("details");
            toolDiv.style.cssText = "font-size:.6875rem;margin:2px 0;";
            var toolData = ev.data && ev.data.result ? JSON.stringify(ev.data.result, null, 2) : "";
            toolDiv.innerHTML = '<summary style="cursor:pointer;color:var(--text-tertiary);">🔧 ' + escapeHTML(ev.name || "") + '</summary>' +
              '<pre class="code-block" style="margin:2px 0 0;max-height:80px;font-size:.625rem;">' + escapeHTML(toolData) + '</pre>';
            toolList.appendChild(toolDiv);
          } else if (ev.type === "assistant" && ev.data) {
            // Show thinking text dimmed.
            var think = document.createElement("div");
            think.style.cssText = "color:var(--text-tertiary);font-size:.75rem;margin:4px 0;";
            think.textContent = String(ev.data).slice(0, 200);
            if (agentContent) agentContent.appendChild(think);
          } else if (ev.type === "done") {
            var finalData = typeof ev.data === "string" ? JSON.parse(ev.data) : ev.data;
            finalAnswer = finalData.answer || finalData.error || "";
            if (finalData.conversation_id && !currentConversationID) {
              currentConversationID = finalData.conversation_id;
            }
            pendingApprovals = finalData.pending_approvals || null;
          }
        } catch (_) {}
      }
      if (msgEl) msgEl.scrollTop = msgEl.scrollHeight;
    }
=======
  try {
    if (elements.taskOutput) elements.taskOutput.textContent = "Agent 正在调用受控工具…";
    if (elements.agentTimeline) elements.agentTimeline.textContent = "运行中…";
    var runResult = await authedRequest("/api/v1/agent/runs", {
      method: "POST",
      body: JSON.stringify({ input: input }),
    });
    if (elements.taskOutput) {
      elements.taskOutput.textContent = runResult.answer || "Agent 未返回结论。";
    }
    renderAgentTimeline(runResult.events || []);
    showToast("Agent 已完成 " + (runResult.steps || 0) + " 个推理步骤");
    await refreshTasksPage();
>>>>>>> origin/main
  } catch (error) {
    if (agentContent) agentContent.innerHTML = '<span style="color:var(--danger);">错误: ' + escapeHTML(error.message) + '</span>';
    showToast(error.message);
  }
  if (indicator) indicator.style.display = "none";

  // Append final answer below thinking + tools, with a separator.
  if (agentContent && finalAnswer) {
    // Remove the initial "思考中…" placeholder.
    var placeholder = agentContent.querySelector("span");
    if (placeholder && placeholder.textContent === "思考中…") placeholder.remove();
    var sep = document.createElement("div");
    sep.style.cssText = "border-top:1px solid var(--border);margin:8px 0;";
    agentContent.appendChild(sep);
    var answer = document.createElement("div");
    answer.className = "md-content";
    answer.innerHTML = markdownToHTML(finalAnswer);
    agentContent.appendChild(answer);
  }

  // Show pending approvals.
  window._adpPendingApprovals = pendingApprovals;
  if (pendingApprovals && pendingApprovals.length > 0) {
    showToast("Agent 等待审批 " + pendingApprovals.length + " 个操作");
    await refreshTasksPage();
  } else {
    showToast("Agent 已完成");
    await loadConversations();
  }
}

async function handleBatchApproval(approvals, approved) {
  if (!ensureAuthed()) return;
  // Clear immediately so buttons disappear.
  window._adpPendingApprovals = null;
  var box = document.getElementById("approval-action-box");
  if (box) box.remove();

  for (var i = 0; i < approvals.length; i++) {
    try {
      await authedRequest("/api/v1/approvals/jobs/" + encodeURIComponent(approvals[i].job_id), {
        method: "POST",
        body: JSON.stringify({ approved: approved, comment: approved ? "Approved" : "Rejected" }),
      });
    } catch (e) { showToast("审批失败: " + e.message); return; }
  }

  var verb = approved ? "已批准" : "已拒绝";
  var jobIds = approvals.map(function(a) { return a.job_id; }).join(", ");
  var followUp = verb + " Job " + jobIds + "。请检查执行结果并继续。";
  showToast(verb + "，Agent 继续执行…");

  // Directly call agent API for continuation.
  var indicator = document.getElementById("agent-running-indicator");
  if (indicator) indicator.style.display = "flex";
  try {
    var body = { input: followUp };
    if (currentConversationID) body.conversation_id = currentConversationID;
    await authedRequest("/api/v1/agent/runs", { method: "POST", body: JSON.stringify(body) });
  } catch (_) {}
  if (indicator) indicator.style.display = "none";
  await refreshTasksPage();
}

async function loadConversations() {
  var listEl = document.getElementById("conversation-list");
  if (!listEl || !state.token) return;
  try {
    var convs = await authedRequest("/api/v1/conversations");
    listEl.innerHTML = "";
    for (var i = 0; i < convs.length; i++) {
      var c = convs[i];
      var active = c.id === currentConversationID;
      var item = document.createElement("div");
      item.className = "list-card" + (active ? " is-active" : "");
      item.style.cssText = "cursor:pointer;margin-bottom:6px;" + (active ? "border-color:var(--accent);" : "");
      item.onclick = function(id) { return function() { selectConversation(id); }; }(c.id);
      item.innerHTML = '<strong style="font-size:.8125rem;">' + escapeHTML(c.title || "新对话") + '</strong>' +
        '<span style="font-size:.6875rem;color:var(--text-tertiary);display:block;">' + formatTime(c.updated_at) + '</span>';
      listEl.appendChild(item);
    }
  } catch (_) {}
}

async function selectConversation(id) {
  currentConversationID = id;
  await refreshTasksPage();
}

function startNewConversation() {
  currentConversationID = "";
  var msgEl = document.getElementById("conversation-messages");
  if (msgEl) msgEl.innerHTML = '<p style="color:var(--text-tertiary);text-align:center;padding:20px;">开始新对话</p>';
  var titleEl = document.getElementById("conv-title-text");
  if (titleEl) titleEl.textContent = "新对话";
  loadConversations();
}

function markdownToHTML(text) {
  if (!text) return "";
  // Escape HTML first, then selectively unescape markdown-formatted content.
  var html = escapeHTML(text);

  // Code blocks (``` ... ```)
  html = html.replace(/```(\w*)\n?([\s\S]*?)```/g, function(_, lang, code) {
    return '<pre class="code-block" style="margin:8px 0;max-height:300px;overflow:auto;"><code>' + code.trim() + '</code></pre>';
  });

  // Inline code (`...`)
  html = html.replace(/`([^`]+)`/g, '<code style="background:var(--bg-tertiary);padding:1px 4px;border-radius:3px;font-family:var(--font-mono);font-size:.8125rem;">$1</code>');

  // Bold (**...**)
  html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');

  // Italic (*...*)
  html = html.replace(/\*(.+?)\*/g, '<em>$1</em>');

  // Headers (### ..., ## ..., # ...)
  html = html.replace(/^### (.+)$/gm, '<h4 style="margin:10px 0 4px;font-size:.875rem;">$1</h4>');
  html = html.replace(/^## (.+)$/gm, '<h3 style="margin:12px 0 4px;font-size:.9375rem;">$1</h3>');
  html = html.replace(/^# (.+)$/gm, '<h2 style="margin:14px 0 6px;font-size:1rem;">$1</h2>');

  // Tables
  html = html.replace(/((?:^\|.+\|$\n?)+)/gm, function(match) {
    var lines = match.trim().split('\n');
    if (lines.length < 2) return match;
    // Skip separator line (|---|---|)
    var rows = [];
    for (var i = 0; i < lines.length; i++) {
      if (/^\|[\s\-:|]+\|$/.test(lines[i])) continue;
      var cells = lines[i].split('|').filter(function(c) { return c.trim() !== ''; });
      var tag = i === 0 ? 'th' : 'td';
      rows.push('<tr>' + cells.map(function(c) { return '<' + tag + ' style="padding:2px 8px;border:1px solid var(--border);">' + c.trim() + '</' + tag + '>'; }).join('') + '</tr>');
    }
    return '<table style="border-collapse:collapse;margin:8px 0;font-size:.8125rem;">' + rows.join('') + '</table>';
  });

  // Unordered lists (- ...)
  html = html.replace(/(?:^- .+$\n?)+/gm, function(match) {
    var items = match.trim().split('\n').map(function(line) {
      return '<li>' + line.replace(/^- /, '') + '</li>';
    }).join('');
    return '<ul style="margin:4px 0;padding-left:20px;">' + items + '</ul>';
  });

  // Paragraphs: double newlines → <br><br>
  html = html.replace(/\n\n/g, '<br><br>');
  html = html.replace(/\n/g, '<br>');

  return html;
}

async function renderConversationMessages() {
  var msgEl = document.getElementById("conversation-messages");
  if (!msgEl || !currentConversationID) return;
  try {
    var msgs = await authedRequest("/api/v1/conversations/" + encodeURIComponent(currentConversationID) + "/messages");
    msgEl.innerHTML = "";
    for (var i = 0; i < msgs.length; i++) {
      var m = msgs[i];
      if (m.role === "tool") {
        // Tool messages: compact, centered
        var tdiv = document.createElement("div");
        tdiv.style.cssText = "text-align:center;margin:4px 0;";
        tdiv.innerHTML = '<details style="display:inline-block;font-size:.6875rem;color:var(--text-tertiary);cursor:pointer;background:var(--surface-inset);padding:3px 10px;border-radius:10px;">' +
          '<summary>🔧 ' + escapeHTML(m.tool_name || "tool") + '</summary>' +
          '<pre class="code-block" style="margin:4px 0 0;max-height:100px;font-size:.625rem;text-align:left;">' + escapeHTML(JSON.stringify(m.tool_data, null, 2)) + '</pre></details>';
        msgEl.appendChild(tdiv);
      } else if (m.role === "user") {
        // User: right-aligned bubble
        var row = document.createElement("div");
        row.style.cssText = "display:flex;justify-content:flex-end;margin:8px 0;";
        row.innerHTML = '<div style="max-width:75%;background:var(--accent);color:#fff;padding:8px 14px;border-radius:16px 16px 4px 16px;font-size:.8125rem;line-height:1.55;white-space:pre-wrap;word-break:break-word;">' + escapeHTML(m.content) + '</div>';
        msgEl.appendChild(row);
      } else if (m.role === "assistant") {
        if (m.content) {
          var row2 = document.createElement("div");
          row2.style.cssText = "display:flex;justify-content:flex-start;margin:8px 0;";
          row2.innerHTML = '<div style="max-width:85%;background:var(--surface-inset);border:1px solid var(--border);padding:10px 14px;border-radius:16px 16px 16px 4px;font-size:.8125rem;line-height:1.6;"><div class="md-content">' + markdownToHTML(m.content) + '</div></div>';
          msgEl.appendChild(row2);
        }
      }
    }
    // Render pending approval buttons from conversation messages.
    var allApprovals = [];
    for (var k = 0; k < msgs.length; k++) {
      if (msgs[k].role === "tool") {
        var found = extractPendingApprovals(msgs[k].tool_data);
        for (var f = 0; f < found.length; f++) { allApprovals.push(found[f]); }
      }
    }
    // Also merge from the stream's completed result. Deduplicate because the
    // same jobs have already been persisted as tool messages.
    if (window._adpPendingApprovals) {
      for (var w = 0; w < window._adpPendingApprovals.length; w++) { allApprovals.push(window._adpPendingApprovals[w]); }
    }
    // Tool messages are historical snapshots. Check the authoritative pending
    // list so actions disappear as soon as a decision has been recorded.
    var pendingJobs = [];
    try {
      pendingJobs = await authedRequest("/api/v1/approvals/jobs");
    } catch (_) {}
    var pendingByID = {};
    for (var p = 0; p < pendingJobs.length; p++) { pendingByID[pendingJobs[p].id] = pendingJobs[p]; }
    var uniqueApprovals = [];
    var seenApprovalIDs = {};
    for (var a = 0; a < allApprovals.length; a++) {
      var approval = allApprovals[a];
      var approvalID = approval && approval.job_id;
      if (!approvalID || seenApprovalIDs[approvalID] || !pendingByID[approvalID]) continue;
      seenApprovalIDs[approvalID] = true;
      uniqueApprovals.push(Object.assign({}, approval, pendingByID[approvalID]));
    }
    if (uniqueApprovals.length > 0) {
      var box = document.createElement("div");
      box.id = "approval-action-box";
      box.style.cssText = "margin:12px 0;padding:12px;border:2px solid var(--accent);border-radius:8px;background:var(--surface-inset);";
      var listHtml = uniqueApprovals.map(function(a) {
        return '<div style="font-family:var(--font-mono);font-size:.75rem;margin:4px 0;">' +
          escapeHTML(a.job_id || "") + ' — ' + escapeHTML(a.command || a.module_code || "") +
          ' → ' + escapeHTML(a.worker_id || "") +
          '<span style="display:inline-flex;gap:6px;margin-left:8px;vertical-align:middle;">' +
            '<button class="btn btn-xs btn-primary" type="button" data-conversation-job-id="' + escapeHTML(a.job_id) + '" data-conversation-decision="approve">批准</button>' +
            '<button class="btn btn-xs btn-ghost" type="button" style="color:var(--danger);" data-conversation-job-id="' + escapeHTML(a.job_id) + '" data-conversation-decision="reject">拒绝</button>' +
            '<button class="btn btn-xs btn-ghost" type="button" data-conversation-job-id="' + escapeHTML(a.job_id) + '" data-conversation-decision="suggest">建议</button>' +
          '</span></div>';
      }).join("");
      box.innerHTML = '<strong style="color:var(--accent);">⚠ 需要审批</strong>' + listHtml +
        '<div style="margin-top:10px;display:flex;gap:8px;flex-wrap:wrap;">' +
          '<button class="btn btn-primary btn-xs" id="approve-all-btn">批准全部</button>' +
          '<button class="btn btn-xs" id="reject-all-btn" style="color:var(--danger);border-color:var(--danger);">拒绝全部</button>' +
        '</div>';
      msgEl.appendChild(box);
      document.getElementById("approve-all-btn").onclick = function() { handleBatchApproval(uniqueApprovals, true); };
      document.getElementById("reject-all-btn").onclick = function() { handleBatchApproval(uniqueApprovals, false); };
    }
    msgEl.scrollTop = msgEl.scrollHeight;
  } catch (_) {}
}

function extractPendingApprovals(toolData) {
  var out = [];
  if (!toolData) return out;
  // Handle both {ok:true, result:{jobs:[...]}} and {jobs:[...]} directly.
  var r = toolData.result || toolData;
  if (!r || typeof r !== "object") return out;
  var jobs = r.jobs;
  if (!jobs && r.job_id) jobs = [{job_id: r.job_id, approval_required: r.approval_required, status: r.status, worker_id: r.worker_id}];
  if (!jobs || !Array.isArray(jobs)) return out;
  for (var i = 0; i < jobs.length; i++) {
    var j = jobs[i];
    if (j && j.approval_required && j.status === "waiting_approval") {
      out.push(j);
    }
  }
  return out;
}

async function handleConversationApproval(jobID, approved) {
  if (!ensureAuthed()) return;
  // Clear pending approvals so buttons disappear.
  window._adpPendingApprovals = null;
  try {
    await authedRequest("/api/v1/approvals/jobs/" + encodeURIComponent(jobID), {
      method: "POST",
      body: JSON.stringify({ approved: approved, comment: approved ? "Approved" : "Rejected" }),
    });
  } catch (e) { showToast("审批失败: " + e.message); return; }

  var verb = approved ? "已批准" : "已拒绝";
  showToast(verb + "，Agent 继续执行…");

  // Auto-continue in same conversation.
  var followUp = verb + " Job " + jobID + "。请检查执行结果并继续。";
  var indicator = document.getElementById("agent-running-indicator");
  if (indicator) indicator.style.display = "flex";
  try {
    var body = { input: followUp };
    if (currentConversationID) body.conversation_id = currentConversationID;
    await authedRequest("/api/v1/agent/runs", { method: "POST", body: JSON.stringify(body) });
  } catch (_) {}
  if (indicator) indicator.style.display = "none";
  await refreshTasksPage();
}

function handleConversationAction(event) {
  var button = event.target.closest("[data-conversation-job-id]");
  if (!button) return;
  var jobID = button.dataset.conversationJobId;
  if (button.dataset.conversationDecision === "suggest") {
    showSuggestBox(jobID);
    return;
  }
  handleConversationApproval(jobID, button.dataset.conversationDecision === "approve");
}

function showSuggestBox(jobID, workerID) {
  var msgEl = document.getElementById("conversation-messages");
  if (!msgEl) return;
  // Remove existing suggest box if any
  var existing = document.getElementById("suggest-box");
  if (existing) existing.remove();

  var box = document.createElement("div");
  box.id = "suggest-box";
  box.style.cssText = "margin:8px 0;padding:8px;border:1px solid var(--accent);border-radius:6px;background:var(--surface-inset);";
  box.innerHTML = '<p style="font-size:.75rem;margin:0 0 6px;">建议 Agent 调整策略 (Job ' + jobID.slice(-6) + ')</p>' +
    '<textarea id="suggest-text" class="field-textarea" rows="2" style="font-size:.75rem;" placeholder="例如：不要重启服务，先检查错误日志再决定"></textarea>' +
    '<div style="margin-top:6px;display:flex;gap:6px;">' +
      '<button class="btn btn-xs btn-primary" onclick="submitSuggestion(\'' + jobID + '\')">提交建议</button>' +
      '<button class="btn btn-xs btn-ghost" onclick="document.getElementById(\'suggest-box\').remove()">取消</button>' +
    '</div>';
  msgEl.appendChild(box);
  box.scrollIntoView({behavior: "smooth"});
}

async function submitSuggestion(jobID) {
  var textEl = document.getElementById("suggest-text");
  if (!textEl || !textEl.value.trim()) { showToast("请输入建议"); return; }
  var suggestion = "关于 Job " + jobID.slice(-6) + " 的建议：" + textEl.value.trim() + "。请根据这个建议重新评估并调整操作。";
  // Submit as a new agent run in the same conversation
  elements.taskInput.value = suggestion;
  document.getElementById("suggest-box").remove();
  await handleTaskSubmit({preventDefault: function(){}});
}

function renderAgentTimeline(events) {
  if (!elements.agentTimeline) return;
  if (!events.length) { elements.agentTimeline.textContent = "本次没有工具调用。"; return; }
  elements.agentTimeline.innerHTML = "";
  events.forEach(function(event) {
    var item = document.createElement("div");
    item.className = "list-card";
    var title = document.createElement("strong");
    title.textContent = "步骤 " + event.step + " · " + (event.type === "tool" ? "工具：" + event.name : "Agent 推理");
    var detail = document.createElement("pre");
    detail.className = "code-block";
    detail.style.marginTop = "8px";
    detail.style.minHeight = "0";
    detail.textContent = typeof event.data === "string" ? event.data : JSON.stringify(event.data, null, 2);
    item.appendChild(title); item.appendChild(detail); elements.agentTimeline.appendChild(item);
  });
}

function renderAgentTimeline(events) {
  if (!elements.agentTimeline) return;
  if (!events.length) { elements.agentTimeline.textContent = "本次没有工具调用。"; return; }
  elements.agentTimeline.innerHTML = "";
  events.forEach(function(event) {
    var item = document.createElement("div");
    item.className = "list-card";
    var title = document.createElement("strong");
    title.textContent = "步骤 " + event.step + " · " + (event.type === "tool" ? "工具：" + event.name : "Agent 推理");
    var detail = document.createElement("pre");
    detail.className = "code-block";
    detail.style.marginTop = "8px";
    detail.style.minHeight = "0";
    detail.textContent = typeof event.data === "string" ? event.data : JSON.stringify(event.data, null, 2);
    item.appendChild(title); item.appendChild(detail); elements.agentTimeline.appendChild(item);
  });
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

function startJobsAutoRefresh() {
  if (page !== "jobs" || !state.token || state.refreshTimer) return;

  var refresh = async function() {
    if (document.hidden || state.refreshInFlight || !state.token) return;
    state.refreshInFlight = true;
    try {
      // Preserve the create-form selections while only the job statuses refresh.
      await refreshJobsPage(false);
    } catch (_) {
      // A transient polling failure should not interrupt the page or create a
      // toast every interval; regular user actions still surface errors.
    } finally {
      state.refreshInFlight = false;
    }
  };

  state.refreshTimer = window.setInterval(refresh, 2000);
  document.addEventListener("visibilitychange", function() {
    if (!document.hidden) refresh();
  });
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

async function refreshJobsPage(refreshOptions) {
  var summary = await authedRequest("/api/v1/dashboard/summary");
  state.user = summary.user;
  updateSessionState(summary.current_time);

  var jobs = await authedRequest("/api/v1/jobs?limit=16");
  if (refreshOptions !== false) {
    var templateSel = document.getElementById("job-template");
    var workerSel = document.getElementById("job-worker");
    if (templateSel) await loadTemplateOptions(templateSel);
    if (workerSel) await loadWorkerDropdown(workerSel);
  }
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
          '<span style="font-size: 0.75rem; color: var(--text-secondary); margin-left: 8px;">' + escapeHTML(job.template_code || "Legacy job") + '</span>' +
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

<<<<<<< HEAD
  await loadConversations();
  await renderConversationMessages();
=======
  var tasks = await authedRequest("/api/v1/jobs?source_type=agent");
>>>>>>> origin/main

  var titleEl = document.getElementById("conv-title-text");
  if (titleEl && currentConversationID) {
    try {
      var conv = await authedRequest("/api/v1/conversations/" + encodeURIComponent(currentConversationID));
      titleEl.textContent = conv.conversation ? conv.conversation.title || "对话" : "对话";
    } catch (_) {}
  }

  var tasks = await authedRequest("/api/v1/jobs?source_type=agent&limit=20");
  renderList(
    elements.taskList,
    tasks,
    function(task) {
      return '<div class="list-card">' +
        '<div style="flex: 1;">' +
          '<strong style="font-size: 0.875rem;">' + escapeHTML(task.name) + '</strong>' +
<<<<<<< HEAD
          '<span style="font-size: 0.75rem; color: var(--text-secondary); margin-left: 8px;">' + escapeHTML(task.template_code || task.source_type || "agent") + '</span>' +
=======
          '<span style="font-size: 0.75rem; color: var(--text-secondary); margin-left: 8px;">' + escapeHTML(task.template_code || "受控 Module") + '</span>' +
>>>>>>> origin/main
        '</div>' +
        '<div class="list-card-meta">' +
          '<span class="status-pill ' + statusClass(task.status) + '"><span class="status-dot"></span>' + escapeHTML(task.status) + '</span>' +
          '<span class="mono">' + escapeHTML(task.template_code || "--") + '</span>' +
          '<span>' + formatTime(task.created_at) + '</span>' +
        '</div>' +
      '</div>';
    },
    "暂无 Agent 创建的操作。"
  );
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
  await refreshSessionOnly();
  if (!state.user || state.user.role !== "admin") {
    if (elements.configsAccessNote) {
      elements.configsAccessNote.textContent = "当前账户不是管理员，无法查看或修改受管配置。";
    }
    renderList(elements.configList, [], function() { return ""; }, "请使用管理员账户登录。");
    return;
  }
  if (elements.configsAccessNote) {
    elements.configsAccessNote.textContent = "当前为管理员，可查看和管理受管配置。";
  }
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
<<<<<<< HEAD
    ["受控能力", summary.templates_total, "YAML 模板 (动态加载)"],
=======
    ["受控能力", summary.templates_total, "可供 Agent 调用的 Module"],
>>>>>>> origin/main
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
          '<span style="font-size: 0.75rem; color: var(--text-secondary); margin-left: 8px;">' + escapeHTML(job.template_code || "Legacy job") + '</span>' +
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
  renderList(elements.taskList, [], function() { return ""; }, "登录后显示 Agent 创建的操作。");
  if (elements.agentTimeline) elements.agentTimeline.textContent = "登录后可查看工具调用时间线。";
  renderList(elements.configList, [], function() { return ""; }, "登录后显示受管配置。");
  renderList(elements.approvalList, [], function() { return ""; }, "登录后显示待审批任务。");
  renderList(elements.auditList, [], function() { return ""; }, "登录后显示审计记录。");
  if (elements.metricsGrid) {
    elements.metricsGrid.innerHTML = "";
  }
  if (elements.taskOutput) {
    elements.taskOutput.textContent = "等待 Agent 请求。";
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

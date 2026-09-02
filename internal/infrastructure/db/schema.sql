-- ADP Database Schema
-- PostgreSQL

-- 用户表
CREATE TABLE IF NOT EXISTS users (
    username   VARCHAR(128) PRIMARY KEY,
    password   VARCHAR(256) NOT NULL,
    role       VARCHAR(32) NOT NULL DEFAULT 'operator',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Worker 表
CREATE TABLE IF NOT EXISTS workers (
    id                  VARCHAR(64) PRIMARY KEY,
    name                VARCHAR(256) NOT NULL,
    worker_type         VARCHAR(128) NOT NULL,
    status              VARCHAR(32) NOT NULL DEFAULT 'offline',
    hostname            VARCHAR(256) NOT NULL DEFAULT '',
    ip_address          VARCHAR(64) NOT NULL DEFAULT '',
    cpu_usage           DOUBLE PRECISION NOT NULL DEFAULT 0,
    storage_usage       DOUBLE PRECISION NOT NULL DEFAULT 0,
    last_heartbeat_at   TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Job 表
CREATE TABLE IF NOT EXISTS jobs (
    id                  VARCHAR(64) PRIMARY KEY,
    name                VARCHAR(512) NOT NULL,
    worker_type         VARCHAR(128) NOT NULL,
    command             TEXT NOT NULL DEFAULT '',
    status              VARCHAR(32) NOT NULL DEFAULT 'pending',
    risk_level          VARCHAR(16) NOT NULL DEFAULT 'low',
    approval_required   BOOLEAN NOT NULL DEFAULT FALSE,
    approval_status     VARCHAR(32) NOT NULL DEFAULT 'not_required',
    approval_comment    TEXT NOT NULL DEFAULT '',
    approved_by         VARCHAR(128) NOT NULL DEFAULT '',
    approved_at         TIMESTAMPTZ,
    rejected_by         VARCHAR(128) NOT NULL DEFAULT '',
    rejected_at         TIMESTAMPTZ,
    template_code       VARCHAR(128) NOT NULL DEFAULT '',
    parameters          JSONB NOT NULL DEFAULT '{}',
    source_type         VARCHAR(64) NOT NULL DEFAULT '',
    source_id           VARCHAR(64) NOT NULL DEFAULT '',
	 idempotency_key     VARCHAR(256) NOT NULL DEFAULT '',
    assigned_worker_id  VARCHAR(64) NOT NULL DEFAULT '',
    output              TEXT NOT NULL DEFAULT '',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at          TIMESTAMPTZ,
    finished_at         TIMESTAMPTZ
);

ALTER TABLE jobs ADD COLUMN IF NOT EXISTS parameters JSONB NOT NULL DEFAULT '{}';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS idempotency_key VARCHAR(256) NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_idempotency_key ON jobs(idempotency_key) WHERE idempotency_key <> '';

-- 诊断计划表
CREATE TABLE IF NOT EXISTS diagnosis_plans (
    id              VARCHAR(64) PRIMARY KEY,
    title           VARCHAR(512) NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    trigger_type    VARCHAR(128) NOT NULL DEFAULT '',
    steps           JSONB NOT NULL DEFAULT '[]',
    status          VARCHAR(32) NOT NULL DEFAULT 'pending',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 审计日志表
CREATE TABLE IF NOT EXISTS audit_logs (
    id              VARCHAR(64) PRIMARY KEY,
    actor_type      VARCHAR(64) NOT NULL,
    actor_id        VARCHAR(128) NOT NULL,
    action          VARCHAR(256) NOT NULL,
    resource_type   VARCHAR(128) NOT NULL,
    resource_id     VARCHAR(128) NOT NULL,
    details         JSONB NOT NULL DEFAULT '{}',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 故障案例表
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS incident_cases (
    id              VARCHAR(64) PRIMARY KEY,
    title           VARCHAR(512) NOT NULL DEFAULT '',
    trigger_type    VARCHAR(128) NOT NULL DEFAULT '',
    fault_type      VARCHAR(256) NOT NULL DEFAULT '',
    summary         TEXT NOT NULL DEFAULT '',
    possible_causes TEXT[] NOT NULL DEFAULT '{}',
    suggestions     TEXT[] NOT NULL DEFAULT '{}',
    confidence      DOUBLE PRECISION NOT NULL DEFAULT 0,
    source_plan_id  VARCHAR(64) NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Iteration 3: normalized historical operations knowledge. Legacy columns
-- remain during migration so old reports and API consumers continue to work.
ALTER TABLE incident_cases ADD COLUMN IF NOT EXISTS alert_symptoms TEXT NOT NULL DEFAULT '';
ALTER TABLE incident_cases ADD COLUMN IF NOT EXISTS environment_tags TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE incident_cases ADD COLUMN IF NOT EXISTS evidence_summary TEXT NOT NULL DEFAULT '';
ALTER TABLE incident_cases ADD COLUMN IF NOT EXISTS root_cause TEXT NOT NULL DEFAULT '';
ALTER TABLE incident_cases ADD COLUMN IF NOT EXISTS resolution_steps TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE incident_cases ADD COLUMN IF NOT EXISTS resolution_result TEXT NOT NULL DEFAULT '';
ALTER TABLE incident_cases ADD COLUMN IF NOT EXISTS status VARCHAR(32) NOT NULL DEFAULT 'approved';
ALTER TABLE incident_cases ADD COLUMN IF NOT EXISTS source_run_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE incident_cases ADD COLUMN IF NOT EXISTS reviewed_by VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE incident_cases ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMPTZ;
ALTER TABLE incident_cases ADD COLUMN IF NOT EXISTS review_note TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_incident_cases_trigger_type ON incident_cases(trigger_type);
CREATE INDEX IF NOT EXISTS idx_incident_cases_fault_type ON incident_cases(fault_type);
CREATE INDEX IF NOT EXISTS idx_incident_cases_environment_tags ON incident_cases USING GIN(environment_tags);
CREATE INDEX IF NOT EXISTS idx_incident_cases_status ON incident_cases(status);

-- One active, versioned embedding per approved incident case.  The source
-- document is rebuilt from reviewed fields, never copied from raw tool logs.
CREATE TABLE IF NOT EXISTS incident_case_embeddings (
    case_id          VARCHAR(64) PRIMARY KEY REFERENCES incident_cases(id) ON DELETE CASCADE,
    content_hash     CHAR(64) NOT NULL,
    embedding_model  VARCHAR(256) NOT NULL,
    dimensions       SMALLINT NOT NULL DEFAULT 1024 CHECK (dimensions = 1024),
    embedding        vector(1024),
    status           VARCHAR(32) NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'ready', 'failed')),
    attempts         INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
ALTER TABLE incident_case_embeddings ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
-- Migrate the initial 1536-dimensional corpus to the 1024-dimensional
-- Cloudflare bge-m3 deployment. Existing vectors cannot be projected safely,
-- so they are discarded and regenerated from the reviewed source fields.
ALTER TABLE incident_case_embeddings DROP CONSTRAINT IF EXISTS incident_case_embeddings_dimensions_check;
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM pg_attribute a
        JOIN pg_class c ON c.oid = a.attrelid
        WHERE c.relname = 'incident_case_embeddings'
          AND a.attname = 'embedding'
          AND a.attnum > 0
          AND a.atttypmod <> 1024
    ) THEN
        DROP INDEX IF EXISTS idx_incident_case_embeddings_hnsw;
        UPDATE incident_case_embeddings
           SET embedding = NULL,
               dimensions = 1024,
               status = 'queued',
               attempts = 0,
               next_attempt_at = NOW(),
               last_error = '',
               updated_at = NOW();
        ALTER TABLE incident_case_embeddings
            ALTER COLUMN embedding TYPE vector(1024) USING embedding::vector(1024);
    END IF;
END $$;
ALTER TABLE incident_case_embeddings ALTER COLUMN dimensions SET DEFAULT 1024;
ALTER TABLE incident_case_embeddings
    ADD CONSTRAINT incident_case_embeddings_dimensions_check CHECK (dimensions = 1024);
CREATE INDEX IF NOT EXISTS idx_incident_case_embeddings_queue ON incident_case_embeddings(status, updated_at);
CREATE INDEX IF NOT EXISTS idx_incident_case_embeddings_hnsw ON incident_case_embeddings
    USING hnsw (embedding vector_cosine_ops) WHERE status = 'ready';

-- Job YAML 模板表
CREATE TABLE IF NOT EXISTS job_yamls (
    id              VARCHAR(64) PRIMARY KEY,
    name            VARCHAR(256) NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    yaml_content    TEXT NOT NULL DEFAULT '',
    source          VARCHAR(32) NOT NULL DEFAULT 'manual',  -- 'ai' | 'manual'
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Worker 执行日志表
CREATE TABLE IF NOT EXISTS worker_logs (
    id          BIGSERIAL PRIMARY KEY,
    worker_id   VARCHAR(64) NOT NULL,
    job_id      VARCHAR(64) NOT NULL,
    command     TEXT NOT NULL DEFAULT '',
    progress    VARCHAR(256) NOT NULL DEFAULT '',
    result      TEXT NOT NULL DEFAULT '',
    success     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 运行时 YAML 配置表：模板、策略、提示词、诊断计划定义
CREATE TABLE IF NOT EXISTS managed_configs (
    id              VARCHAR(128) NOT NULL,
    kind            VARCHAR(64) NOT NULL,
    name            VARCHAR(256) NOT NULL DEFAULT '',
    yaml_content    TEXT NOT NULL DEFAULT '',
    active          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (kind, id)
);

-- ID 序列表 (用于生成自增ID)
CREATE TABLE IF NOT EXISTS id_sequences (
    prefix      VARCHAR(64) PRIMARY KEY,
    next_value  BIGINT NOT NULL DEFAULT 1
);

-- 索引
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
CREATE INDEX IF NOT EXISTS idx_jobs_worker_type ON jobs(worker_type);
CREATE INDEX IF NOT EXISTS idx_jobs_source_type ON jobs(source_type);
CREATE INDEX IF NOT EXISTS idx_jobs_assigned_worker ON jobs(assigned_worker_id);
CREATE INDEX IF NOT EXISTS idx_workers_type ON workers(worker_type);
CREATE INDEX IF NOT EXISTS idx_audit_resource ON audit_logs(resource_type, resource_id);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_incident_cases_trigger ON incident_cases(trigger_type);
CREATE INDEX IF NOT EXISTS idx_incident_cases_fault ON incident_cases(fault_type);
CREATE INDEX IF NOT EXISTS idx_worker_logs_worker ON worker_logs(worker_id);
CREATE INDEX IF NOT EXISTS idx_worker_logs_job ON worker_logs(job_id);
CREATE INDEX IF NOT EXISTS idx_managed_configs_kind ON managed_configs(kind);

-- 对话表
CREATE TABLE IF NOT EXISTS conversations (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS conversation_messages (
    id BIGSERIAL PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    content TEXT NOT NULL DEFAULT '',
    tool_name TEXT DEFAULT '',
    tool_data JSONB DEFAULT '{}',
    step INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_conv_msgs_cid ON conversation_messages(conversation_id, id);

-- Durable Agent execution ledger. Transcript contains only LLM protocol messages;
-- events and tool calls remain separately queryable for replay and audit.
CREATE TABLE IF NOT EXISTS agent_runs (
    id VARCHAR(64) PRIMARY KEY,
    input TEXT NOT NULL,
    conversation_id VARCHAR(64) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL,
    trace_id VARCHAR(128) NOT NULL,
    policy_version VARCHAR(128) NOT NULL,
    prompt_version VARCHAR(128) NOT NULL,
    transcript JSONB NOT NULL DEFAULT '[]',
    next_step INTEGER NOT NULL DEFAULT 1,
    answer TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_agent_runs_status ON agent_runs(status);
CREATE TABLE IF NOT EXISTS agent_events (
    id BIGSERIAL PRIMARY KEY,
    run_id VARCHAR(64) NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step INTEGER NOT NULL DEFAULT 0,
    type VARCHAR(64) NOT NULL,
    name VARCHAR(128) NOT NULL DEFAULT '',
    data JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_agent_events_run_id ON agent_events(run_id, id);
CREATE TABLE IF NOT EXISTS agent_tool_calls (
    id VARCHAR(128) PRIMARY KEY,
    run_id VARCHAR(64) NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step INTEGER NOT NULL,
    tool_name VARCHAR(128) NOT NULL,
    arguments JSONB NOT NULL DEFAULT '{}',
    result JSONB NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_agent_tool_calls_run_id ON agent_tool_calls(run_id, step);

-- Sanitized prompt projections are immutable evidence of what a model saw at
-- each step. They are distinct from the canonical recovery transcript.
CREATE TABLE IF NOT EXISTS agent_context_snapshots (
    id BIGSERIAL PRIMARY KEY,
    run_id VARCHAR(64) NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    step INTEGER NOT NULL,
    transcript_version INTEGER NOT NULL,
    token_estimate INTEGER NOT NULL,
    budget_tokens INTEGER NOT NULL DEFAULT 0,
    decisions JSONB NOT NULL DEFAULT '{}',
    messages JSONB NOT NULL DEFAULT '[]',
    content_sha256 VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_agent_context_snapshots_run_step ON agent_context_snapshots(run_id, step, id);

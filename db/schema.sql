-- 红薯创作台元数据表。
-- 约定：时间统一为 Unix 毫秒，布尔值用 0/1，账号删除后保留墓碑行并清空身份。

-- 数据库版本使用 SQLite 内置的 PRAGMA user_version 记录，不单独建表。

-- 本地账号及其绑定的小红书身份、登录状态和当前 profile 指针。
CREATE TABLE IF NOT EXISTS accounts (
  id TEXT PRIMARY KEY,                       -- 本地账号 ID，32 位十六进制
  name TEXT NOT NULL,                        -- 用户设置的账号备注名
  user_id TEXT NOT NULL DEFAULT '',          -- 已验证的小红书用户 ID，未绑定为空
  nickname TEXT NOT NULL DEFAULT '',         -- 最近一次核验到的昵称
  avatar TEXT NOT NULL DEFAULT '',           -- 最近一次核验到的头像地址
  is_logged_in INTEGER NOT NULL DEFAULT 0    -- 最近一次明确检查的登录状态
    CHECK (is_logged_in IN (0, 1)),
  login_checked_at INTEGER,                  -- 最近一次登录检查时间，Unix 毫秒，NULL 表示未检查
  seed INTEGER NOT NULL,                     -- 该账号固定的浏览器指纹 seed
  active_profile TEXT NOT NULL DEFAULT '',   -- 当前使用的 profile 目录 ID
  created_at INTEGER NOT NULL,               -- 创建时间，Unix 毫秒
  updated_at INTEGER NOT NULL,               -- 最后更新时间，Unix 毫秒
  deleted INTEGER NOT NULL DEFAULT 0         -- 墓碑标记，1 表示已移除
    CHECK (deleted IN (0, 1))
);

-- 同一真实小红书账号只能绑定一个未删除的本地账号。
CREATE UNIQUE INDEX IF NOT EXISTS accounts_user_id_unique
  ON accounts(user_id)
  WHERE user_id <> '' AND deleted = 0;

-- 按存活状态和创建时间读取账号列表。
CREATE INDEX IF NOT EXISTS accounts_alive
  ON accounts(deleted, created_at);

-- 全工作台共享的模型设置，只允许一行。
CREATE TABLE IF NOT EXISTS settings (
  id INTEGER PRIMARY KEY CHECK (id = 1),     -- 固定为 1 的单行配置
  initialized INTEGER NOT NULL DEFAULT 0     -- 是否已完成初始化
    CHECK (initialized IN (0, 1)),
  text_base_url TEXT NOT NULL DEFAULT '',    -- 文字模型接口地址
  text_model TEXT NOT NULL DEFAULT '',       -- 文字模型名称
  text_api_key TEXT NOT NULL DEFAULT '',     -- 文字模型 API Key，明文依赖文件权限保护
  image_base_url TEXT NOT NULL DEFAULT '',   -- 生图模型接口地址
  image_model TEXT NOT NULL DEFAULT '',      -- 生图模型名称
  image_api_key TEXT NOT NULL DEFAULT '',    -- 生图模型 API Key，明文依赖文件权限保护
  updated_at INTEGER NOT NULL                -- 最后更新时间，Unix 毫秒
);

-- 每个本地账号的服务端草稿，payload 保存现有前端草稿 JSON。
CREATE TABLE IF NOT EXISTS drafts (
  account_id TEXT PRIMARY KEY                -- 所属本地账号
    REFERENCES accounts(id) ON DELETE CASCADE,
  payload TEXT NOT NULL,                     -- 草稿 JSON，包含文字、资料和图片引用
  revision INTEGER NOT NULL DEFAULT 0        -- 草稿版本号，用于多窗口冲突检测
    CHECK (revision >= 0),
  updated_at INTEGER NOT NULL                -- 最后保存时间，Unix 毫秒
);

-- 每个本地账号的发布防重记录，跨浏览器共享。
CREATE TABLE IF NOT EXISTS publish_protection (
  account_id TEXT PRIMARY KEY                -- 所属本地账号
    REFERENCES accounts(id) ON DELETE CASCADE,
  revision INTEGER NOT NULL DEFAULT 0        -- 记录版本号，用于条件更新
    CHECK (revision >= 0),
  uncertain INTEGER NOT NULL DEFAULT 0       -- 上次发布结果是否尚未确认
    CHECK (uncertain IN (0, 1)),
  last_published TEXT NOT NULL DEFAULT '',   -- 最近成功发布内容的指纹
  reviewed_revision INTEGER,                 -- 用户核对发布结果时的记录版本
  updated_at INTEGER NOT NULL                -- 最后更新时间，Unix 毫秒
);

-- schema version 2
-- 后台发布任务保留提交快照，账号墓碑不会删除历史。
CREATE TABLE IF NOT EXISTS publish_tasks (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL REFERENCES accounts(id),
  account_name TEXT NOT NULL,
  user_id TEXT NOT NULL,
  kind TEXT NOT NULL CHECK (kind IN ('publish', 'publish_video')),
  title TEXT NOT NULL,
  payload TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  request_id TEXT NOT NULL,
  protection_revision INTEGER NOT NULL CHECK (protection_revision > 0),
  status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'uncertain')),
  message TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  result TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  started_at INTEGER,
  finished_at INTEGER
);

CREATE UNIQUE INDEX IF NOT EXISTS publish_tasks_request_unique
  ON publish_tasks(account_id, request_id);

-- 同账号只允许一个尚未结束的任务，保护并发入队。
CREATE UNIQUE INDEX IF NOT EXISTS publish_tasks_active_unique
  ON publish_tasks(account_id) WHERE status IN ('queued', 'running');

CREATE INDEX IF NOT EXISTS publish_tasks_newest
  ON publish_tasks(created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS publish_tasks_queued
  ON publish_tasks(created_at, id) WHERE status = 'queued';

-- schema version 3
-- 定时配置保存账号身份快照；删除只留墓碑，不删除执行历史。
CREATE TABLE IF NOT EXISTS scheduled_jobs (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL CHECK (length(trim(name)) > 0),
  account_id TEXT NOT NULL REFERENCES accounts(id),
  account_name TEXT NOT NULL,
  user_id TEXT NOT NULL DEFAULT '',
  keyword TEXT NOT NULL CHECK (length(trim(keyword)) > 0),
  topic TEXT NOT NULL CHECK (length(trim(topic)) > 0),
  instructions TEXT NOT NULL DEFAULT '',
  publication_type TEXT NOT NULL DEFAULT 'image' CHECK (publication_type IN ('image', 'long_text')),
  cron TEXT NOT NULL,
  timezone TEXT NOT NULL,
  reference_count INTEGER NOT NULL DEFAULT 20 CHECK (reference_count BETWEEN 1 AND 20),
  enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
  next_run_at INTEGER,
  last_run_at INTEGER,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  deleted INTEGER NOT NULL DEFAULT 0 CHECK (deleted IN (0, 1)),
  CHECK (enabled = 0 OR (deleted = 0 AND user_id <> '' AND next_run_at IS NOT NULL)),
  CHECK (enabled = 1 OR next_run_at IS NULL)
);

CREATE INDEX IF NOT EXISTS scheduled_jobs_due
  ON scheduled_jobs(next_run_at, id) WHERE enabled = 1 AND deleted = 0;

-- snapshot 保存完整配置和私有身份，公开 JSON 不包含 user_id。
CREATE TABLE IF NOT EXISTS schedule_runs (
  id TEXT PRIMARY KEY,
  schedule_id TEXT NOT NULL REFERENCES scheduled_jobs(id),
  account_id TEXT NOT NULL REFERENCES accounts(id),
  account_name TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('running', 'publishing', 'succeeded', 'failed', 'uncertain', 'skipped')),
  stage TEXT NOT NULL CHECK (stage IN ('searching', 'reading', 'composing', 'imaging', 'publishing', 'finished')),
  scheduled_at INTEGER NOT NULL,
  started_at INTEGER NOT NULL,
  finished_at INTEGER,
  title TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL DEFAULT '',
  tags TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(tags) AND json_type(tags) = 'array'),
  sources TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(sources) AND json_type(sources) = 'array'),
  error TEXT NOT NULL DEFAULT '',
  publish_task_id TEXT NOT NULL DEFAULT '',
  snapshot TEXT NOT NULL CHECK (json_valid(snapshot) AND json_type(snapshot) = 'object'),
  image_prompt TEXT NOT NULL DEFAULT '',
  images TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(images) AND json_type(images) = 'array'),
  retry_of_id TEXT NOT NULL DEFAULT '',
  CHECK ((status IN ('running', 'publishing') AND finished_at IS NULL AND stage <> 'finished')
    OR (status IN ('succeeded', 'failed', 'uncertain', 'skipped') AND finished_at IS NOT NULL AND stage = 'finished')),
  CHECK (status <> 'publishing' OR (publish_task_id <> '' AND stage = 'publishing'))
);

CREATE UNIQUE INDEX IF NOT EXISTS schedule_runs_occurrence_unique
  ON schedule_runs(schedule_id, scheduled_at) WHERE retry_of_id = '';

CREATE UNIQUE INDEX IF NOT EXISTS schedule_runs_active_account_unique
  ON schedule_runs(account_id) WHERE status IN ('running', 'publishing');

CREATE UNIQUE INDEX IF NOT EXISTS schedule_runs_active_schedule_unique
  ON schedule_runs(schedule_id) WHERE status IN ('running', 'publishing');

CREATE UNIQUE INDEX IF NOT EXISTS schedule_runs_retry_unique
  ON schedule_runs(retry_of_id) WHERE retry_of_id <> '';

CREATE INDEX IF NOT EXISTS schedule_runs_newest
  ON schedule_runs(schedule_id, started_at DESC);

-- schema version 4
-- 定时创作只保留关键词，历史执行快照及参考来源保持原样。
ALTER TABLE scheduled_jobs DROP COLUMN topic;
ALTER TABLE scheduled_jobs DROP COLUMN instructions;
ALTER TABLE scheduled_jobs DROP COLUMN reference_count;

-- schema version 5
-- 创作要求可选，已有关键词任务默认留空。
ALTER TABLE scheduled_jobs ADD COLUMN instructions TEXT NOT NULL DEFAULT '';

-- schema version 6
-- 安全验证独立于最近明确的登录结果，已有账号默认无需验证。
ALTER TABLE accounts ADD COLUMN verification_required INTEGER NOT NULL DEFAULT 0
  CHECK (verification_required IN (0, 1));

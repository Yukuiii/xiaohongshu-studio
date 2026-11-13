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

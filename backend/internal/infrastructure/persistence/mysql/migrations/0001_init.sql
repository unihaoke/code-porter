-- 0001_init.sql — CodePorter 多租户账号体系初始结构
--
-- 约定：
--   * 所有表 InnoDB + utf8mb4；时间统一 DATETIME(6)（DSN 强制 parseTime=true）。
--   * users 删除时级联清理秘钥/会话/Agent 身份（ON DELETE CASCADE）。
--   * 秘钥采用硬删除：api_keys 查不到记录即等同吊销，因此不设 revoked 列。
--   * 文件末尾以 INSERT IGNORE 种子化首个 admin（admin / admin123），
--     已改密的环境重复执行迁移不会被覆盖。

CREATE TABLE IF NOT EXISTS users (
    id            VARCHAR(48)  NOT NULL,
    username      VARCHAR(64)  NOT NULL,
    password_hash VARCHAR(100) NOT NULL,
    `role`        VARCHAR(16)  NOT NULL DEFAULT 'member',
    `status`      VARCHAR(16)  NOT NULL DEFAULT 'active',
    created_at    DATETIME(6)  NOT NULL,
    updated_at    DATETIME(6)  NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_users_username (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS api_keys (
    id           VARCHAR(48)  NOT NULL,
    user_id      VARCHAR(48)  NOT NULL,
    name         VARCHAR(128) NOT NULL,
    key_hash     CHAR(64)     NOT NULL,
    prefix       VARCHAR(16)  NOT NULL,
    scopes       VARCHAR(32)  NOT NULL,
    expires_at   DATETIME(6)      NULL,
    last_used_at DATETIME(6)      NULL,
    created_at   DATETIME(6)  NOT NULL,
    updated_at   DATETIME(6)  NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_api_keys_hash (key_hash),
    KEY idx_api_keys_user (user_id),
    CONSTRAINT fk_api_keys_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS sessions (
    token_hash   CHAR(64)     NOT NULL,
    user_id      VARCHAR(48)  NOT NULL,
    created_at   DATETIME(6)  NOT NULL,
    expires_at   DATETIME(6)  NOT NULL,
    last_seen_at DATETIME(6)  NOT NULL,
    PRIMARY KEY (token_hash),
    KEY idx_sessions_user (user_id),
    KEY idx_sessions_expires (expires_at),
    CONSTRAINT fk_sessions_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Agent 只持久化身份（归属、名称、注册与最近出现时间）；
-- 在线状态/心跳/健康快照是易失运行时态，留在网关内存，重连即恢复。
CREATE TABLE IF NOT EXISTS agents (
    id           VARCHAR(48)  NOT NULL,
    user_id      VARCHAR(48)  NOT NULL,
    name         VARCHAR(128) NOT NULL,
    created_at   DATETIME(6)  NOT NULL,
    last_seen_at DATETIME(6)  NOT NULL,
    PRIMARY KEY (id),
    KEY idx_agents_user (user_id),
    CONSTRAINT fk_agents_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- 默认管理员（首次部署种子；INSERT IGNORE 保证改密后重复迁移不覆盖）。
-- 默认密码 admin123（bcrypt cost 10），登录后请立即通过个人菜单修改。
INSERT IGNORE INTO users (id, username, password_hash, role, status, created_at, updated_at)
VALUES ('usr_admin_seed', 'admin',
        '$2a$10$edJivRxk2uDGRk1wo81P9.2ZCVYJ/HpOZebun1oa5K2XOYoettfdq',
        'admin', 'active', UTC_TIMESTAMP(6), UTC_TIMESTAMP(6));

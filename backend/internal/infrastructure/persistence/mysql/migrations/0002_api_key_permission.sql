-- 0002_api_key_permission.sql — 秘钥增加文件操作权限（read/write/all）
--
-- 与 scopes 正交：scopes 决定能调哪类接口（agent/api），
-- permission 决定通过该秘钥下发的任务在本地机器上的文件操作上限：
--   read  只读（禁止改文件与副作用命令）
--   write 仅工作目录可写
--   all   读写与命令执行全开（默认，兼容存量秘钥）
--
-- 存量行由列默认值自动补为 all，无需回填。

ALTER TABLE api_keys
    ADD COLUMN permission VARCHAR(16) NOT NULL DEFAULT 'all' AFTER scopes;

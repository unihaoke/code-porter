-- 0003_drop_bots.sql — 下线服务端 IM 机器人（网关 Webhook 模式）
--
-- 机器人能力已迁移到本地客户端：飞书 SDK 长连接在 Agent 进程内收发消息，
-- 不再经过网关 /webhook 回调与 MySQL 配置表。网关侧的菜单、接口与本表现一并移除。
DROP TABLE IF EXISTS bots;

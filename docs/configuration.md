# 配置

本文说明环境变量、`.env` 文件和 Telegram BotFather 的必需设置。运行方式见 [部署](deployment.md)，命令与默认群设置见 [行为说明：命令](behavior.md#命令)。

## 环境变量

| 变量 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `BOT_TOKEN` | 是 | 无 | Telegram Bot Token；未设置时程序启动失败 |
| `DB_PATH` | 否 | `bot.db` | SQLite 数据库路径；Docker 镜像内默认为 `/data/bot.db` |
| `DEDUP_SECRET` | 否 | 回退到 `BOT_TOKEN` | 文本去重 HMAC 密钥；更换后历史文本的重复记录将无法命中 |
| `STATS_TZ` | 否 | `Asia/Shanghai` | 排行榜周界使用的时区；时区名非法时回退到 UTC（`Asia/Shanghai` / `CST` 回退为固定 UTC+8） |
| `DATA_RETENTION_DAYS` | 否 | `180` | 数据保留天数；非法或非正数回退到 180。启动时清理一次，之后每 24 小时清理 |
| `PROXY_URL` | 否 | 无 | 访问 Telegram 的代理，支持 `http://`、`https://`、`socks5://`；格式无效或不支持的协议会被忽略 |

程序启动时通过 `godotenv` 读取工作目录下的 `.env`（如果存在），因此本地运行前建议从 `.env.example` 复制一份。

`.env.example` 已列出 `BOT_TOKEN`、`DB_PATH`，以及可选的 `DEDUP_SECRET`、`STATS_TZ`、`DATA_RETENTION_DAYS`、`PROXY_URL` 示例。`.env` 已被 `.gitignore` 与 `.dockerignore` 排除，不会被提交或打进构建上下文。

## BotFather 注意事项

机器人要读取群里的**全部**消息，否则隐私模式会让它只收到命令、@ 提及和回复：

1. 在 [@BotFather](https://t.me/BotFather) 中打开该 Bot，执行 `/setprivacy`，选择 **Disable**。
2. 修改隐私模式后，需要把 Bot **移出群再重新加入**，设置才会生效。
3. 或者把 Bot 设为群管理员——管理员 Bot 可以收到群内全部消息。
4. Bot 必须留在群内；管理员命令依赖 `getChatMember` 查询操作者权限。

## 默认设置

默认设置：检测开启、冷却 0 秒、@ 提醒开启。默认值只有在管理员修改设置后才会写入数据库。各命令及其权限见 [行为说明：命令](behavior.md#命令)。

## 配置变更的影响

- 更换 `DEDUP_SECRET` 后，历史文本键和链接键都无法再与新内容匹配，文本与纯链接的去重计数会从更换后重新开始。媒体键和频道键本身不依赖该密钥，仍可命中；日志中的 `key_hash` 会随密钥变化。
- 修改 `DATA_RETENTION_DAYS` 后，新的保留天数从下一次清理开始生效；启动时会先执行一次清理。
- `STATS_TZ` 影响排行榜、`/mars_me` 和 `/hyw` 的周/月边界与时间展示，不改变已保存的时间戳。
- `PROXY_URL` 只用于访问 Telegram；格式无效时记录警告并忽略代理。

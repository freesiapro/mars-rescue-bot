# 火星救援 Bot

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

一个用 Go 编写的 Telegram 群聊搬石/抵消检测机器人：当群里反复出现同一内容时，它会发送一条“火星救援”提醒，并把重复发送者计入火星指数排行榜。项目使用 Go + GORM + SQLite，单实例即可运行。

机器人会忽略私聊消息，目标使用场景是群聊。

## 核心特性

- **重复检测与配对提醒**：文本、纯链接、富文本、图片/视频/动画/文件、频道转发都参与检测；同一内容按出现顺序两两配对，通常第二次出现时提醒（连续刷屏等例外见 [行为说明](docs/behavior.md)）。
- **火星指数排行榜**：`/mars_rank` 和 `/mars_me` 按重复出现统计（同一条内容首次出现不计入），支持本周、本月和全部历史。
- **只读查询**：回复任意消息发送 `/hyw`，查看是否被记录、内容类型、8 位指纹、累计次数和可推导的决策。
- **群内治理**：开关检测、冷却、忽略用户、控制是否 @ 重复发送者。
- **隐私优先**：数据库只保存内容键、Telegram ID、计数和群设置，不保存消息原文、链接原文或媒体内容；数据默认保留 180 天。

详细规则和边界见 [行为说明](docs/behavior.md)。

## 快速开始

要求 Go 1.26.1 或更高版本（以 `go.mod` 为准）。

```bash
cp .env.example .env
# 编辑 .env，至少填入 BOT_TOKEN
go run .
```

数据库默认创建在当前目录的 `bot.db`，并自动建表。机器人需要读取群内全部消息，使用前请按 [BotFather 注意事项](docs/configuration.md#botfather-注意事项) 关闭隐私模式，或用管理员身份把 Bot 拉进群。

Docker、Compose、数据卷和备份方式见 [部署](docs/deployment.md)；完整环境变量见 [配置](docs/configuration.md)。

## 文档

| 文档 | 内容 |
| --- | --- |
| [行为说明](docs/behavior.md) | 检测范围、归一化、纯链接/富文本/mention-only、配对、排行榜、命令、已知限制 |
| [配置](docs/configuration.md) | 环境变量、`.env`、BotFather、配置变更影响 |
| [部署](docs/deployment.md) | 本地运行、Docker、Compose、数据目录权限、备份与回滚 |
| [运维](docs/operations.md) | 数据与隐私、决策日志、`/hyw`、保留清理、排障 |
| [开发](docs/development.md) | 目录结构、测试、贡献 |

完整目录见 [docs/README.md](docs/README.md)。

## 许可证

本项目采用 [MIT 许可证](LICENSE)。

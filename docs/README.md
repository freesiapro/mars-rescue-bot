# 文档索引

本目录保存火星救援 Bot 的详细文档；根目录 [README](../README.md) 只保留项目概览、快速开始和文档索引。

| 文档 | 内容 |
| --- | --- |
| [行为说明](behavior.md) | 参与检测的内容、文本/链接/富文本/mention-only 规则、内容键与归一化、配对、排行榜、命令、已知限制 |
| [配置](configuration.md) | 环境变量、`.env`、BotFather 隐私模式、默认设置与配置变更影响 |
| [部署](deployment.md) | 本地运行、Docker、Docker Compose、数据目录权限、备份与回滚 |
| [运维](operations.md) | 保存的数据与隐私、运行日志、去重决策日志、`/hyw`、数据保留与清理、排障 |
| [开发](development.md) | 目录结构、测试命令、贡献注意事项 |

## 阅读顺序

- 第一次部署：先看 [配置](configuration.md) 和 [部署](deployment.md)，再看 [行为说明](behavior.md) 了解检测范围。
- 线上排查：先看 [运维](operations.md) 的决策日志和 `/hyw`，再对照 [行为说明](behavior.md) 的规则。
- 参与开发：先看 [开发](development.md) 的目录结构和测试命令。

## 许可证

本项目采用 [MIT 许可证](../LICENSE)。

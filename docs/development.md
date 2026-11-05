# 开发

本文说明仓库结构、本地验证方式和贡献注意事项。项目概览见 [README](../README.md)，行为规则见 [行为说明](behavior.md)。

## 目录结构

```text
main.go                  程序入口：配置、数据库、Telegram 客户端、维护任务
internal/bot             消息检测、内容键、归一化、配对、提醒与决策日志
internal/handler         命令注册、消息处理、管理员操作、/hyw 与隐私说明
internal/repo            GORM 数据访问：记录、流水、统计、群设置、忽略名单
internal/models          GORM 模型
internal/config          环境变量读取与默认值
internal/database        SQLite 初始化、PRAGMA、AutoMigrate
internal/maintenance     保留期清理任务
internal/logger          JSON 日志与 trace 中间件
internal/names           进程内用户显示名缓存，按需查询 getChatMember
internal/textnorm        文本归一化与有效内容判断
internal/testutil        测试用内存数据库
```

顶层还包括 `Dockerfile`、`docker-compose.yml`、`.env.example`、`LICENSE` 和本目录下的文档。

## 测试

```bash
go build ./...
go vet ./...
go test ./...
gofmt -l .
```

`gofmt -l .` 没有输出表示所有 Go 文件格式正确。

测试使用 `t.TempDir()` 下的临时 SQLite 文件，不访问 Telegram，也不读写真实数据库文件。新增检测分支时，应同时补充处理器、决策日志和 `/hyw` 相关测试，避免检测规则与查询展示漂移。

## 贡献

- 提交前运行上面的 `go build`、`go vet`、`go test` 和 `gofmt`，确保全部通过且没有格式差异。
- 不要提交 `.env`、数据库文件（`*.db`、`*.sqlite` 等）或任何密钥。`.gitignore` 已排除这些文件，`.dockerignore` 也排除了 `.env`。
- 修改检测、配对、日志字段或命令时，同步更新 `README.md` 和 `docs/` 中对应说明；决策日志的 `decision` / `msg_type` 取值变化必须与 [运维：去重决策日志](operations.md#去重决策日志) 保持一致。
- 数据库结构通过 `AutoMigrate` 管理；变更模型时确认旧数据库可以正常启动，并考虑线上已有数据的兼容性。
- 新增环境变量时，同时更新 [配置：环境变量](configuration.md#环境变量)、`.env.example` 和 README 的快速开始（如涉及）。

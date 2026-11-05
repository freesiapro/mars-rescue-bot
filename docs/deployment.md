# 部署

本文说明本地运行、Docker、Docker Compose、数据目录权限，以及 SQLite 数据的备份与回滚。环境变量和 BotFather 设置见 [配置](configuration.md)。

## 本地运行

要求 Go 1.26.1 或更高版本（以 `go.mod` 为准）。

```bash
cp .env.example .env
# 编辑 .env，至少填入 BOT_TOKEN
go run .
```

也可以先构建再运行：

```bash
go build -o mars-rescue-bot .
./mars-rescue-bot
```

数据库会创建在 `DB_PATH` 指定的位置（默认当前目录下的 `bot.db`），并自动建表。

## Docker 运行

镜像为多阶段构建：Go 构建阶段生成静态二进制，运行阶段使用 Alpine，并以非 root 用户（UID/GID `10001`）运行。镜像中不包含任何密钥，token 必须通过环境变量传入。

```bash
docker build -t mars-rescue-bot .
docker run -d \
  --name mars-rescue-bot \
  --env-file .env \
  -e DB_PATH=/data/bot.db \
  -v mars-data:/data \
  --restart unless-stopped \
  mars-rescue-bot
```

## Docker Compose

或使用仓库中的 `docker-compose.yml`：

```bash
docker compose up -d --build
docker compose logs -f
```

Compose 示例已经：

- 通过 `env_file: .env` 注入配置；
- 用 `environment: DB_PATH: /data/bot.db` 覆盖 `.env` 中的相对路径；
- 将命名卷 `mars-data` 挂载到 `/data`；
- 设置 `restart: unless-stopped`。

如果改用宿主机目录绑定挂载（例如 `./data:/data`），需要确保该目录对容器内 UID `10001` 可写：

```bash
mkdir -p data
sudo chown -R 10001:10001 data
```

`.env` 已被 `.gitignore` 与 `.dockerignore` 排除，不会被提交或打进构建上下文。

## 数据与备份

SQLite 数据库位于 `DB_PATH`（Docker 镜像和 Compose 示例中为 `/data/bot.db`），数据库使用 WAL 模式，因此同目录下可能同时存在 `bot.db-wal` 和 `bot.db-shm`。仓库没有内置备份命令，备份时按文件处理：

1. 先停止 Bot：本地运行时发送 `SIGINT`/`SIGTERM`，Docker 使用 `docker stop`，Compose 使用 `docker compose stop`。程序收到退出信号后会优雅停止并关闭数据库。
2. 复制 `DB_PATH` 指向的数据库文件；如果存在 `-wal` / `-shm` 文件，一并复制。
3. 恢复时先停止 Bot，用备份覆盖同名文件及其 `-wal` / `-shm`，再启动。启动时 `AutoMigrate` 会检查并补齐表结构。

Docker 命名卷的数据可以通过一个临时容器复制出来，例如：

```bash
docker run --rm -v mars-data:/data -v "$PWD":/backup alpine \
  tar czf /backup/mars-data.tar.gz -C /data .
```

不要把同一个 SQLite 文件同时挂给多个 Bot 实例；连接数和写入都按单实例设计，相关限制见 [行为说明：已知限制](behavior.md#已知限制)。

## 回滚

- **只回滚程序**：停止当前实例，换回上一版二进制或镜像后启动。启动时会执行 `AutoMigrate`；如果新版本写过旧版本不认识的列，旧版本会忽略这些列，但降级前仍建议先备份数据库。
- **回滚数据**：按上一节的步骤停止 Bot，用备份的数据库文件及 `-wal` / `-shm` 覆盖当前文件，再启动。
- 升级或降级前都应先备份 `DB_PATH`，并确保同一时间只有一个实例在写该数据库。

## 相关文档

- 环境变量、`.env`、BotFather：[配置](configuration.md)
- 检测规则、配对与已知限制：[行为说明](behavior.md)
- 日志、数据保留与 `/hyw`：[运维](operations.md)

# 运维

本文说明机器人保存的数据、运行日志与去重决策日志、`/hyw` 查询、数据保留清理和常见排查。检测规则见 [行为说明](behavior.md)，环境变量见 [配置](configuration.md)，部署与备份见 [部署](deployment.md)。

## 隐私与数据

数据库（SQLite）中保存的内容（群内可发送 `/mars_privacy` 查看）：

- **文本内容键**：`HMAC-SHA256(DEDUP_SECRET, 归一化后的文本)` 的十六进制结果，不保存原文或归一化后的文本。富文本消息先提取去除格式后的纯文本，再按同一流程计算。未设置 `DEDUP_SECRET` 时回退使用 `BOT_TOKEN` 作为密钥。
- **链接内容键**：纯链接消息使用 `link:v1:` 前缀。链接按出现顺序、每个只做 scheme/host 小写后，以换行连接，再计算 `HMAC-SHA256(DEDUP_SECRET, ...)`；数据库和日志都不保存链接原文。
- **Telegram ID**：群 ID、用户 ID、消息 ID；频道转发的内容键中还包含原频道 ID 与原消息 ID；媒体内容键中包含 `file_unique_id`。内容记录还保存最近一次出现的消息 ID，仅用于识别连续刷屏。
- **排行榜数据**：只保存用户 ID 与出现次数。姓名和 username 不写入数据库；展示排行榜、首发者时使用进程内缓存，未命中再调用 Telegram `getChatMember`，查询失败显示“用户 &lt;id&gt;”。升级时会清空旧版本 `user_stats` 中已经保存的 `username`/`first_name`，列本身保留以兼容旧数据库。
- **群设置**：群 ID、开关、冷却秒数、操作者用户 ID。
- **忽略名单**：群 ID、被忽略用户 ID、操作者用户 ID、添加时间。
- **时间戳**：记录的创建与更新时间。

不保存：消息原文、链接原文、媒体文件内容、文件下载链接。媒体消息只保存不可逆的 `file_unique_id` 用于去重；纯链接消息只保存 HMAC 内容键。机器人不保存原始消息 JSON；`present_fields` / `rich_block_types` 来自 telebot 已解析的 `Message` 结构，只包含字段名和块类型，不包含字段值或正文。

运行日志以 JSON 输出到 stdout，包含群 ID、用户 ID、消息 ID、消息类型等元数据，不包含用户名或消息正文。

## 去重决策日志

每条经过去重处理的消息都会额外输出一条 INFO 日志，消息固定为 `dedup decision`。它与原有的 `收到消息` 日志并列：前者说明检测结果，后者记录收到的更新，每条消息各一条，不重复输出。字段只有：

| 字段 | 说明 |
| --- | --- |
| `chat_id` | 群 ID；缺少 chat 时为 0 |
| `message_id` | 消息 ID；缺少消息时为 0 |
| `msg_type` | `channel_forward`、`rich_message`、`text`、`link`、`photo`、`video`、`animation`、`document`、`audio`、`sticker`、`voice`、`location`、`contact`、`caption` 或 `unknown`；频道转发优先于原始内容类型 |
| `decision` | 本次处理结果，取值见下表 |
| `user_id` | 发送者 ID，与 `收到消息` 日志一致；缺少发送者时为 0 |
| `key_hash` | 可选。内容键的 8 位十六进制短指纹；没有内容键时省略 |
| `present_fields` | 可选。仅 `no_content` / `unsupported_type` 时输出：消息中非空顶层字段名（排序、去重；`null`、空字符串、空数组/对象、`false`、`0` 不计），不包含字段值和正文。使用 telebot 解析后的 `Message` 结构，看不到 telebot 未建模的字段；富文本路由用的空白占位 `text` 会被排除 |
| `rich_block_types` | 可选。仅当消息带 `rich_message` 且决策为 `no_content` / `unsupported_type` 时输出：块树中出现过的 `type` 名称（排序、去重，含嵌套块），不包含任何文本 |

`key_hash` 对文本和链接直接使用现有 HMAC-SHA256 内容键的前 8 位；媒体和频道键本身不是 HMAC（包含 `file_unique_id` 或原频道 ID），因此使用 `HMAC-SHA256(DEDUP_SECRET, 完整内容键)` 后再取前 8 位，避免明文暴露这些标识。日志永远不包含完整内容键、消息正文、caption、用户名或姓名。

| `decision` | 产生分支 |
| --- | --- |
| `alerted` | 本次出现完成配对，且提醒发送成功 |
| `paired_silent` | 新内容首次记录，或本次出现进入等待配对、不提醒 |
| `adjacent_repeat_exempt` | 与上一次出现的 `message_id` 相差不超过 5，按连续刷屏只计数、不提醒 |
| `short_text_skipped` | 归一化后有效字符少于 10 个的文本，或纯表情/符号；纯链接走链接键，不会命中该分支 |
| `repeated_char_skipped` | 归一化后所有字符都相同（如“哈哈哈哈哈哈哈哈哈哈”“6666666666”） |
| `mention_only` | 去掉 @ 提及（`mention` / `text_mention` 实体或文本中的 `@username`）后只剩空白、标点或符号；对所有来源（含频道转发）生效，不检测、不记录、不参与配对 |
| `bot_ignored` | 发送者本身是 bot |
| `forward_from_bot_ignored` | 保留值，当前不可达：只有发送者是 bot 才会被忽略；人类转发 bot 内容仍按普通内容去重 |
| `ignored_user` | 发送者在忽略名单中 |
| `disabled_chat` | 本群检测已关闭 |
| `cooldown_suppressed` | 配对提醒被群冷却抑制，配对状态已回滚 |
| `send_failed_rolled_back` | 配对提醒发送失败，配对状态已回滚 |
| `no_content` | 没有可检测内容：空文本、纯空白，或支持的媒体缺少 `file_unique_id`。日志会附带 `present_fields` |
| `private_chat` | 私聊消息，直接忽略 |
| `command` | 以 `/` 开头的命令文本 |
| `unsupported_type` | 音频、贴纸、语音、位置、联系人，或没有任何可提取文本的富文本消息等未参与检测的内容。日志会附带 `present_fields`，富文本消息还会附带 `rich_block_types` |
| `no_message` / `no_chat` / `no_sender` | Telegram 更新缺少消息、chat 或发送者，安全跳过 |
| `processing_error` | 读取设置、忽略名单或写入去重数据时出错；原有错误日志仍会输出 |

## 数据保留与清理

数据保留：默认保留 **180 天**，可通过 `DATA_RETENTION_DAYS` 调整。程序启动时执行一次清理，之后每 24 小时执行一次：

- 删除 `created_at` 早于保留期的 `forward_logs`；
- 先删除过期记录残留的流水，再删除 `updated_at` 早于保留期的 `forward_records`，避免孤儿流水；
- 删除不再被任何保留期流水引用的 `user_stats` 行（该表只含用户 ID）。

清理按批次执行，不会一次性锁住数据库。`/mars_rank all` 和 `/mars_me` 的“累计”只统计仍在保留期内的流水。

<a id="hyw"></a>

## 查询一条消息（/hyw）

在群里**回复**任意一条消息，再发送 `/hyw`（也支持 `/hyw@botname`），机器人会回复被查询的消息：

- 有没有被火星小本本记上；
- 内容类型：文本、链接、图片、视频、动图、文件或频道转发；
- 8 位内容指纹（就是决策日志里的 `key_hash`；不显示完整内容键、`file_unique_id`、频道 ID 或消息原文）；
- `ForwardCount` 累计出现次数（包含已被保留期清理掉流水的历史次数）；
- 首次和最近一次出现的时间，格式与提醒一致：当天 `12:30`，其他日期 `11-03 12:30`。

这是**只读**查询：不会新增/修改 `forward_records`、`forward_logs`、`user_stats`，不会改变配对状态、最近消息 ID 或任何设置，也不会发送提醒。查询只限当前群；其他群记录不会命中。`/hyw` 自身是命令，沿用命令豁免：不记录、不计数，只产生一条 `decision=command` 的决策日志。

数据库没有逐条保存“这条消息当时是什么决策”，只保存记录、出现流水和当前配对状态。因此回复里的“决策”只根据现有证据推导，推导不出来的会直接写“没法确定”：

| 显示 | 推导规则 |
| --- | --- |
| `paired_silent（第一次见，先记下）` | 被查询的消息 ID 等于记录的 `first_target_msg_id`，一定是首次出现 |
| `adjacent_repeat_exempt（跟上一波连着刷，没打扰）` | 能在流水里找到它的上一条，且两条消息 ID 相差 0–5 |
| `alerted（这次提醒发出去了）` | 它是最近一次出现（`last_occurrence_msg_id`，缺失时回退到最后一条流水），且记录当前没有待配对（`pending_pair=false`；旧行按 `ForwardCount` 奇偶推导） |
| `没法确定（...）` | 流水里找不到这条、它不是最近一次、或最近一次仍有待配对。后两种无法区分静默配对、冷却抑制和发送失败回滚 |

补充说明：

- 流水超过保留期被清理后，旧消息可能只剩累计次数，无法再定位单条消息，此时会提示流水已过保留期。
- 升级前已有记录没有 `last_occurrence_msg_id`；升级后的第一次出现仍按普通配对分支处理，但流水里前后消息 ID 可能相邻，因此 `/hyw` 可能把它显示为 `adjacent_repeat_exempt`。这是查询侧的证据限制，不影响当时的实际配对。
- 没有回复消息、在私聊使用、回复 bot 消息，或回复的是命令、只有 @ 的消息、过短文本、纯复读字符、空内容、音频等不参与去重的内容时，机器人会给出简短说明或用法提示。空内容或未支持类型还会列出被回复消息中非空的顶层字段名（如 `rich_message`、`forward_origin`），只列字段名，不显示字段值或正文。

## 排障

- **群里收不到普通消息**：检查 BotFather 隐私模式是否已关闭，修改后是否把 Bot 移出群再重新加入；或把 Bot 设为管理员。见 [配置：BotFather 注意事项](configuration.md#botfather-注意事项)。
- **只有命令有反应**：同上，通常是隐私模式仍开启。
- **命令没有反应**：确认是在群聊中使用；管理员命令要求操作者是群管理员或群主，权限通过 `getChatMember` 实时查询。私聊消息会被忽略。
- **重复内容没有提醒**：检查本群是否被 `/mars_off` 关闭、发送者是否在忽略名单、是否处于冷却期、是否属于相邻消息 ID 相差 ≤5 的连续刷屏，或内容本身是否参与检测。可用 `/hyw` 回复该消息查看记录和可推导决策，或查看 `dedup decision` 日志中的 `decision`。
- **`/hyw` 显示“没法确定”**：这是查询侧证据限制，不代表消息没有被检测；具体原因见上面的推导表和补充说明。
- **数据库报错或锁住**：确认只有一个 Bot 实例在写同一个 SQLite 文件；数据库按单实例、单连接设计。见 [行为说明：已知限制](behavior.md#已知限制)。
- **时间或排行榜周期不对**：检查 `STATS_TZ`；非法时区会回退。见 [配置：环境变量](configuration.md#环境变量)。
- **日志在哪里**：运行日志以 JSON 输出到 stdout；Docker 使用 `docker compose logs -f` 查看。

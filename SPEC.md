# SPEC

## 服务边界

- 仅支持 macOS
- 仅支持单用户
- 支持飞书机器人单聊文本、单聊图片、群聊 `@` 文本
- 仅监听 `127.0.0.1`
- 默认通过 `codex -a never exec -s danger-full-access` 执行
- 群聊权限默认自动获取，`authorized_group_chat_ids` 仅用于启动导入
- 群聊 v1 不接收图片

## 消息流

1. 飞书长连接收到 `im.message.receive_v1`
2. 单聊接受 `message_type = text|image`
3. 群聊只接受 `chat_type = group` 且消息里带 `@`
4. 未授权单聊用户仅允许通过 `/pair` 发起配对申请
5. 使用 `message_id` 做幂等去重
6. 先给原消息添加 `Typing` reaction 作为 ack
7. 如为图片消息，先下载到本机应用目录
8. 加载最近 N 条会话，构造成统一 prompt
9. 调用本机 `codex -a never exec`
10. 解析最终输出中的 `[[image:/absolute/path]]` 标记
11. 先回复文本，再按顺序发送图片
12. 删除第 6 步添加的 ack reaction
13. 记录消息、执行、会话、授权与配对状态到 SQLite

## 配对机制

- `authorized_open_id` 仍为启动时的初始授权用户
- 服务启动时自动把 `authorized_open_id` 写入授权用户表
- 未授权单聊用户发送 `/pair` 会创建或刷新一条 pending 申请
- `/pair` 的回复会直接给出一条 server 主机侧 approve `curl`
- 本机 API 可对申请执行 approve / reject
- approve 后该 open_id 进入授权用户表

## 群权限机制

- bot 被加入群时，自动把该 `chat_id` 写入授权群表
- bot 被移出群或群解散时，自动移除该 `chat_id`
- 如历史群未收到加群事件，首次收到该群的 `@` 文本时自动补录
- `authorized_group_chat_ids` 只作为启动时的 bootstrap 种子

## 本地 API

### GET /v1/healthz

响应：

```json
{
  "ok": true
}
```

### GET /v1/status

响应字段：

- `service`
- `http_addr`
- `default_work_dir`
- `authorized_open_id`：脱敏后的值
- `ws_running`
- `ws_connected`
- `last_connected_at`
- `last_event_at`
- `last_error`
- `last_execution`
- `recent_context_limit`

约束：

- 不返回任何飞书密钥
- 不返回完整授权 Open ID

### POST /v1/messages/send

请求：

```json
{
  "text": "hello from codex",
  "image_paths": ["/absolute/path/to/file.png"]
}
```

行为：

- 只向 `authorized_open_id` 发送消息
- 支持文本、图片，或两者同时发送
- 成功后返回发送出去的 `message_ids`

### GET /v1/conversations/recent?limit=N

响应：

- `items`：最近会话记录
- `count`：返回条数

约束：

- 默认 limit 取服务配置值
- 按时间升序返回最近结果，便于直接拼上下文

### GET /v1/pairing/requests

响应：

- `items`：待确认的配对申请
- `count`：返回条数

### POST /v1/pairing/requests/{open_id}/approve

行为：

- 将指定配对申请标记为 approved
- 把对应 open_id 写入授权用户表

### POST /v1/pairing/requests/{open_id}/reject

行为：

- 将指定配对申请标记为 rejected

## 持久化

SQLite 表：

- `processed_messages`
- `conversation_entries`
- `executions`
- `authorized_users`
- `authorized_groups`
- `pairing_requests`

约束：

- `processed_messages.message_id` 为主键，用于幂等
- `executions.request_message_id` 唯一
- 所有时间统一保存为 UTC RFC3339Nano

新增字段：

- `processed_messages.chat_type`
- `processed_messages.message_type`
- `processed_messages.raw_content_json`
- `conversation_entries.content_type`
- `conversation_entries.file_path`

## 错误处理

- ack reaction 发送失败：记录日志，不阻断后续执行，也不回退成文本 ack
- ack reaction 删除失败：记录日志，不影响最终回复
- 图片下载失败：记录错误并终止该条消息处理
- 单张图片发送失败：记录日志，继续尝试发送剩余图片
- 当最终文本为空且图片都未成功发送时，补发一条失败说明文本
- `codex -a never exec` 失败：最终回复一条失败说明，同时把原始错误记入执行记录
- 最终回复失败：有限重试，默认 3 次
- 非白名单单聊用户：仅 `/pair` 生效，其他消息忽略
- 群聊未 `@`、或图片消息：直接忽略

# SPEC

## 服务边界

- 仅支持 macOS
- 仅支持单用户
- 仅支持飞书机器人 1:1 私聊文本消息
- 仅监听 `127.0.0.1`
- 默认通过 `codex -a never exec -s danger-full-access` 执行

## 消息流

1. 飞书长连接收到 `im.message.receive_v1`
2. 只接受 `chat_type = p2p` 且 `message_type = text`
3. 只接受 `sender_open_id == authorized_open_id`
4. 使用 `message_id` 做幂等去重
5. 先给原消息添加 `Typing` reaction 作为 ack
6. 加载最近 N 条会话，构造成统一 prompt
7. 调用本机 `codex -a never exec`
8. 把最终文本回复到原消息
9. 删除第 5 步添加的 ack reaction
10. 记录消息、执行、会话到 SQLite

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
  "text": "hello from codex"
}
```

行为：

- 只向 `authorized_open_id` 发送文本消息
- 成功后返回发送出去的 `message_id`

### GET /v1/conversations/recent?limit=N

响应：

- `items`：最近会话记录
- `count`：返回条数

约束：

- 默认 limit 取服务配置值
- 按时间升序返回最近结果，便于直接拼上下文

## 持久化

SQLite 表：

- `processed_messages`
- `conversation_entries`
- `executions`

约束：

- `processed_messages.message_id` 为主键，用于幂等
- `executions.request_message_id` 唯一
- 所有时间统一保存为 UTC RFC3339Nano

## 错误处理

- ack reaction 发送失败：记录日志，不阻断后续执行，也不回退成文本 ack
- ack reaction 删除失败：记录日志，不影响最终回复
- `codex -a never exec` 失败：最终回复一条失败说明，同时把原始错误记入执行记录
- 最终回复失败：有限重试，默认 3 次
- 非白名单用户：直接忽略，不触发 `codex -a never exec`
- 非文本或非私聊消息：直接忽略

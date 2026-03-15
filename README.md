# Feishu Codex Bridge

把飞书消息直接桥接到本机 Codex 的轻量服务。

适合一个人长期在 macOS 上使用：在飞书里给机器人发消息，本机收到后调用 `codex` 执行，再把结果回发到飞书。

## 项目作用

- 通过飞书机器人接收单聊消息和群聊 `@` 文本消息
- 调用本机 `codex -a never exec`
- 在执行期间给原消息添加 `Typing` reaction
- 把最终文本或图片结果回复到原消息
- 提供仅限本机访问的 HTTP API，方便其他本地工具复用

## 主要特性

- 单聊支持动态配对，保留 `authorized_open_id` 作为初始授权用户
- 群聊权限自动获取，`authorized_group_chat_ids` 仅作为启动导入种子
- 单聊支持收图、发图
- 自动记录消息、执行结果和最近会话上下文
- 支持 `launchd` 常驻运行
- 默认监听 `127.0.0.1:8787`
- 默认工作目录为 `$HOME/Service`

## 快速开始

### 1. 准备配置

默认配置路径：

`~/Library/Application Support/feishu-codex-bridge/config.yaml`

最小必填项：

- `app_id`
- `app_secret`
- `authorized_open_id`
- 如需预置历史群权限，可设置 `authorized_group_chat_ids`
- 如需覆盖默认表情，可设置 `ack_reaction_type`

建议先复制样例：

```bash
mkdir -p "$HOME/Library/Application Support/feishu-codex-bridge"
cp ./config/config.example.yaml \
  "$HOME/Library/Application Support/feishu-codex-bridge/config.yaml"
```

然后填写真实值。

### 1.1 消息规则

- 单聊：
  - 已授权用户可发送文本和图片
  - 未授权用户发送 `/pair` 可发起配对申请，并收到一条 server 主机侧 `curl` 命令
- 群聊：
  - bot 被拉入群后会自动获得该群权限
  - 首次收到未知群的 `@` 文本时也会自动补录该群权限
  - 只响应明确 `@` 机器人的文本消息
  - v1 不接收群聊图片

### 1.2 Codex 发图约定

如果希望桥接层把本机图片发回飞书，Codex 需要在最终输出里使用显式路径标记，每行一个：

```text
[[image:/absolute/path/to/file.png]]
```

桥接层会剥离这些标记行，把剩余文本作为文本回复，再按顺序发送图片。

### 1.3 Codex 直接写飞书 Wiki 约定

如果希望桥接层把 Markdown 直接写入飞书 Wiki / Docx 页面，Codex 需要输出一个显式块：

```text
[[wiki-write:https://example.feishu.cn/wiki/xxxx]]
# 标题
正文
[[/wiki-write]]
```

说明：

- 支持 `https://.../wiki/<token>` 和 `https://.../docx/<token>` 链接
- 桥接层会把块内 Markdown 覆盖写入目标页面
- 块外普通文本仍会作为聊天回复发送
- 如果只有写页面动作、没有额外文本，桥接层会默认回复：`已写入飞书 Wiki 页面。`

### 2. 本地运行

```bash
cd /path/to/feishu-codex-bridge
go mod tidy
./scripts/build.sh
./bin/feishu-codex-bridge
```

如果不先构建，也可以直接：

```bash
cd /path/to/feishu-codex-bridge
go run ./cmd/feishu-codex-bridge
```

### 3. 配置常驻运行

```bash
cd /path/to/feishu-codex-bridge
./scripts/install_launchd.sh
```

查看状态：

```bash
launchctl list | grep feishu-codex-bridge
```

卸载：

```bash
cd /path/to/feishu-codex-bridge
./scripts/uninstall_launchd.sh
```

## 本地 API

- `GET /v1/healthz`
- `GET /v1/status`
- `POST /v1/messages/send`
- `GET /v1/conversations/recent?limit=N`
- `GET /v1/pairing/requests`
- `POST /v1/pairing/requests/{open_id}/approve`
- `POST /v1/pairing/requests/{open_id}/reject`

`POST /v1/messages/send` 请求示例：

```json
{
  "text": "hello from codex",
  "image_paths": ["/absolute/path/to/file.png"]
}
```

默认监听：

`127.0.0.1:8787`

## 日志与数据

- SQLite：`~/Library/Application Support/feishu-codex-bridge/bridge.db`
- 日志：`~/Library/Logs/feishu-codex-bridge/`

## 仓库结构

- `cmd/feishu-codex-bridge`：程序入口
- `internal/`：核心实现
- `config/config.example.yaml`：配置样例
- `launchd/`：LaunchAgent 模板
- `scripts/`：构建、运行、安装脚本

## 常见问题

### 1. 服务启动失败

优先检查：

- 配置文件路径是否存在
- `authorized_open_id` 是否已填写
- `codex` 路径是否正确

### 2. 能连接飞书但收不到消息

检查飞书开放平台是否已开启消息事件订阅，并发布包含相关权限的版本。

如果是群聊场景，再检查是否明确 `@` 了机器人。

如果要使用“直接写飞书 Wiki 页面”，还需要确认当前飞书应用版本已开通并发布 Wiki / Docx 读取与编辑文档相关权限。

### 3. 收到 `/pair` 但仍无法使用

检查：

- 该配对申请是否已通过本机 API 批准
- `GET /v1/pairing/requests` 是否仍显示为 pending
- 回复里的 `curl` 是否在 server 主机上执行成功

### 4. 能收消息但无法执行 Codex

检查：

- `codex -a never exec --help` 在当前用户下是否可执行
- launchd 的 `PATH` 是否包含 `codex` 所在目录
- `stdout.log` 和 `stderr.log` 中是否有错误

# Feishu Codex Bridge

这是一个运行在 macOS 上的本地飞书长连接桥接服务。

## 目标

- 通过飞书机器人接收你的 1:1 私聊文本消息
- 默认使用 `$HOME/Service` 调用本机 `codex -a never exec`
- 先给原消息添加一个 `Typing` reaction，并在最终回复后移除
- 在 Codex 完成后把最终结果回发到飞书
- 暴露仅限 `127.0.0.1` 的本地 API，供 `$feishu-bridge` skill 调用

## 为什么不用 Docker Compose

v1 明确不以 `docker compose` 作为正式部署形态。

原因：

- 这是单用户、本机、macOS 常驻服务，`launchd` 和系统登录态天然匹配
- 服务需要直接调用本机 `codex` CLI，本地路径和用户环境比容器更自然
- 长连接服务更需要开机自启和崩溃拉起，`launchd` 成本更低

## 目录

- `cmd/feishu-codex-bridge`：程序入口
- `internal/`：桥接服务内部实现
- `config/config.example.yaml`：配置样例
- `launchd/`：LaunchAgent 模板
- `scripts/`：构建、运行、安装 launchd

## 配置

默认配置路径：

`~/Library/Application Support/feishu-codex-bridge/config.yaml`

最小必填项：

- `app_id`
- `app_secret`
- `authorized_open_id`
- 如需覆盖默认表情，可设置 `ack_reaction_type`

建议先复制样例：

```bash
mkdir -p "$HOME/Library/Application Support/feishu-codex-bridge"
cp ./config/config.example.yaml \
  "$HOME/Library/Application Support/feishu-codex-bridge/config.yaml"
```

然后填写真实值。

## 本地运行

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

## launchd 安装

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

默认监听：

`127.0.0.1:8787`

## 日志与数据

- SQLite：`~/Library/Application Support/feishu-codex-bridge/bridge.db`
- 日志：`~/Library/Logs/feishu-codex-bridge/`

## 常见问题

### 1. 服务启动失败

优先检查：

- 配置文件路径是否存在
- `authorized_open_id` 是否已填写
- `codex` 路径是否正确

### 2. 能连接飞书但收不到消息

检查飞书开放平台是否已开启消息事件订阅，并发布包含相关权限的版本。

### 3. 能收消息但无法执行 Codex

检查：

- `codex -a never exec --help` 在当前用户下是否可执行
- launchd 的 `PATH` 是否包含 `codex` 所在目录
- `stdout.log` 和 `stderr.log` 中是否有错误

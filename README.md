# Feishu Codex Bridge

把飞书私聊消息直接桥接到本机 Codex 的轻量服务。

适合一个人长期在 macOS 上使用：在飞书里给机器人发消息，本机收到后调用 `codex` 执行，再把结果回发到飞书。

## 项目作用

- 通过飞书机器人接收 1:1 私聊文本消息
- 调用本机 `codex -a never exec`
- 在执行期间给原消息添加 `Typing` reaction
- 把最终结果回复到原消息
- 提供仅限本机访问的 HTTP API，方便其他本地工具复用

## 主要特性

- 单用户白名单控制，只处理指定 `authorized_open_id`
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
- 如需覆盖默认表情，可设置 `ack_reaction_type`

建议先复制样例：

```bash
mkdir -p "$HOME/Library/Application Support/feishu-codex-bridge"
cp ./config/config.example.yaml \
  "$HOME/Library/Application Support/feishu-codex-bridge/config.yaml"
```

然后填写真实值。

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

### 3. 能收消息但无法执行 Codex

检查：

- `codex -a never exec --help` 在当前用户下是否可执行
- launchd 的 `PATH` 是否包含 `codex` 所在目录
- `stdout.log` 和 `stderr.log` 中是否有错误

# 本地端管协议与模拟管侧

模拟管侧直接实现 AgentCenter 对 Agent 暴露的 gRPC 接口，并用本地 HTTP 提供演示组件文件。Go 模块在 [local-agent-demo/manager](local-agent-demo/manager)，复用仓库内 Agent 的生成代码和 Snappy 压缩器。监听地址为 gRPC `127.0.0.1:6751` 和动态分配的 HTTP 回环端口。gRPC 双向 TLS 使用仓库内测试证书，Agent 校验服务端证书中的 `elkeid.com`，模拟管侧验证客户端证书。参见 [Agent 连接配置](../agent/transport/connection/product.go) 和 [证书校验](../agent/transport/connection/connection.go)。

## 构建与验收

在 Linux 环境中从仓库根目录运行。Agent 构建方法见 [构建和运行](local-agent-build.md)。

```bash
repo="$(pwd -P)"
mkdir -p "$HOME/elkeid-local-demo"
(
  cd "$repo/docs/local-agent-demo/manager"
  go test ./...
  go build -o "$HOME/elkeid-local-demo/mock-manager" .
)
bash "$repo/docs/local-agent-demo/verify.sh" \
  "$HOME/elkeid-local-demo/agent/elkeid-agent" \
  "$HOME/elkeid-local-demo/mock-manager"
```

验收脚本使用临时工作目录和固定文本上传样本，结束时关闭两个进程并保留日志。手动启动模拟管侧时传入 `-repo "$repo"`，从 Agent 工作目录启动 Agent。可选扫描场景使用 `-scanner-fixture-dir`，准备步骤和命中证据见 [检测、触发与上报](local-agent-detection.md)。

## 上行遥测

`/grpc.Transfer/Transfer` 是双向流。Agent 发送 `PackagedData`，字段 1 是 `records`，字段 2 是 `agent_id`，字段 3–9 依次承载内网和外网 IPv4、内网和外网 IPv6、主机名、版本及产品。每个 `EncodedRecord` 的字段 1 是 `data_type`，字段 2 是 Unix 秒时间戳，字段 3 是编码后的 `Payload`。将这段字节按 `Payload` 解码后，字段 1 是字符串键值映射 `fields`。Agent 对两条 gRPC 流使用 Snappy。参见 [协议定义](../agent/proto/grpc.proto) 和 [Transfer 客户端](../agent/transport/transfer.go)。

| 数据类型 | 含义 | 模拟管侧记录 |
| --- | --- | --- |
| 1000 | Agent 心跳 | `agent_heartbeat`、端侧状态、时间戳 |
| 1001 | 插件心跳 | `plugin_heartbeat`、插件名、时间戳 |
| 9990 | 演示插件启动事件 | `fixture_record`、`message="local fixture started"`、`detail="loopback-fixture"`、时间戳和嵌套 Payload 的十六进制 |
| 6003 | 可选扫描结果 | `scanner_report`、任务 token、分类、签名名、错误字段和时间戳 |

演示组件通过文件描述符 4 写入一个真实的插件管道报文。该报文由四字节小端长度和序列化的 `Record` 组成。Agent 解析管道帧，并将 `Payload` 作为字节放进 `EncodedRecord` 上报。参见 [插件管道启动](../agent/plugin/plugin_linux.go) 和 [插件记录读取](../agent/plugin/plugin.go)。

## 下行配置、任务和文件

- `Command.configs` 是字段 3，表示期望组件列表。模拟管侧在首个有效 Agent 批次到达时发送固定的 `demo-fixture` 配置。配置含组件版本、文件 SHA-256、组件校验摘要、回环下载 URL 和 `detail`。Agent 通过 HTTP 下载原始组件文件，校验 SHA-256，并向子进程提供 `DETAIL`。组件检查环境值后才发出 9990。管侧观察 `config_sent`、`fixture_download` 和 `fixture_record` 可分别验证配置发送、文件下载和端侧生效。参见 [下载与摘要检查](../agent/utils/download.go)。
- `Command.task` 是字段 2，其中 `Task` 的字段 1–4 依次是 `data_type`、`object_name`、`data` 和 `token`。模拟管侧单独发送 1050，目标是 `elkeid-agent`，数据指定 Agent 工作目录下的固定文件 `demo-upload.txt` 和 `buf_size=4096`，token 为 `local-demo-upload`。
- `/grpc.FileExt/Upload` 是客户端流。每个 `FileUploadRequest` 通过字段 1 传 token，字段 2 传文件分块。Agent 完成写入后关闭发送方向并等待 `FileUploadResponse.status`。模拟管侧校验 token 和固定的 25 字节样本内容，成功时返回 `SUCCESS` 并记录 `upload_verified` 及 SHA-256。参见 [上传客户端](../agent/transport/file_ext.go)。
- 可选扫描配置在同一条组件列表中加入已校验的 scanner 包。模拟管侧收到 scanner 心跳后发送命中文本任务，收到对应 6003 后发送干净文本任务。两个任务使用固定本地文本样本，结果以 token 关联。

## 实际报文样例

以下是模拟管侧在真实 Agent 的 `EncodedRecord.data` 中收到的 Payload 十六进制。gRPC 已处理 TLS、消息分帧和 Snappy，这段是内层 Protobuf 的原始字节。

```text
0a1a0a0664657461696c12106c6f6f706261636b2d666978747572650a200a076d65737361676512156c6f63616c20666978747572652073746172746564
```

将同一条记录用 JSON 展示。外层数据类型和时间戳随 EncodedRecord 传输，内层两项是解码出的字符串字段。

```json
{
  "data_type": 9990,
  "timestamp": 1795535882,
  "payload": {
    "fields": {
      "detail": "loopback-fixture",
      "message": "local fixture started"
    }
  }
}
```

该插件只有收到正确的 `DETAIL` 才会上报这个事件。组件配置发送、HTTP 下载和记录出现的顺序可从 [实跑记录](local-agent-integration.md#实跑记录) 追溯。


文件上传用例在同一连接上收到独立的 `Command.task`。以下按字段展开，`data` 在线上仍是一个字符串，Agent 在接收任务后才按 JSON 解析。

```json
{
  "task": {
    "data_type": 1050,
    "object_name": "elkeid-agent",
    "data": "{\"path\":\"demo-upload.txt\",\"buf_size\":4096}",
    "token": "local-demo-upload"
  }
}
```

随后 FileExt 收到同一 token 和文件字节 `Elkeid local demo upload\n`，并回传 `status=SUCCESS`。其中 `\n` 表示一个 LF 字节。

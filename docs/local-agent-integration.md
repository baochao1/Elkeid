# Elkeid Agent 本地端管对接

本实验使用仓库内的 Linux Agent 和仅监听回环地址的模拟 AgentCenter。当前仓库包含 AgentCenter、Manager 和 ServiceDiscovery 源码。模拟端直接实现 Agent 所需的 RPC，便于单独验证端管链路。生产服务的部署、身份认证、持久化、任务编排和规则引擎超出本实验范围。

## 场景入口

| 场景 | 操作与协议说明 | 验收观察 |
| --- | --- | --- |
| 构建与本地安装 | [构建和运行](local-agent-build.md) | Linux 二进制启动，Agent 与模拟 AgentCenter 完成双向 TLS |
| gRPC 心跳与上报 | [端管协议与模拟管侧](local-agent-protocol.md) | 收到 Agent 心跳，并解码真实插件管道转发的业务记录 |
| 文件上传与下载 | [端管协议与模拟管侧](local-agent-protocol.md) | 固定测试文件经 FileExt 上传，演示组件经 HTTP 下载并通过摘要校验 |
| 组件策略与管控 | [端管协议与模拟管侧](local-agent-protocol.md) | 管端发送配置，端侧按配置启动组件，并从上报观察生效 |
| IPS 与 AV | [检测、触发与上报](local-agent-detection.md) | 以检测组件实际产出、Agent 转发记录和管侧接收为完整链路证据 |

## 管控流程

1. 管理侧维护组件版本和适用范围。在完整系统中，Manager 根据 Agent 上下文解析组件策略，AgentCenter 根据 Agent ID 找到连接，并通过已有的 Transfer 双向流下发命令。参见 [组件策略](../server/manager/biz/handler/v6/component.go)、[命令入口](../server/agent_center/httptrans/http_handler/command.go) 和 [双向流处理](../server/agent_center/grpctrans/grpc_handler/transfer_handler.go)。
2. Agent 把含 Task 的命令分派给 Agent 内置处理器或目标插件，处理后结束本条命令。同步组件使用单独的 Command，携带完整的期望组件列表。参见 [Agent 命令分派](../agent/transport/transfer.go)。
3. 插件管理器按组件名和版本比对当前实例。相同版本且进程仍在运行时复用现有实例，版本变化时停止旧进程，配置列表移除组件时停止并清理组件目录。Agent 检查本地文件摘要，必要时下载组件，再启动子进程。配置项的 detail 通过启动环境中的 DETAIL 交给插件。只修改同版本配置的 detail 不会重启正在运行的插件。参见 [插件配置同步](../agent/plugin/plugin.go) 和 [Linux 插件加载](../agent/plugin/plugin_linux.go)。
4. 以插件实际产生的遥测和 Agent 日志判断端侧是否生效。AgentCenter 成功写入流只能证明命令已发送。任务结果的格式和 token 回传取决于具体任务或插件，组件同步没有统一成功回执。

## 读报文

Transfer 上行是 PackagedData，其中 records 由若干 EncodedRecord 组成。每条记录带数据类型、Unix 秒时间戳和 Protobuf Payload 字节。Payload 解码后的 fields 是字符串映射。心跳和插件业务记录复用这层封装。下行是 Command。FileExt 上传使用单独的 gRPC RPC，组件文件下载使用配置给出的 HTTP 地址。参见 [Agent 协议定义](../agent/proto/grpc.proto)。

## 实跑记录

在 WSL 中用原版 Agent `1.0.0-local` 执行 [验收脚本](local-agent-demo/verify.sh)，脚本退出码为 `0`。摘录如下，省略时间戳和与复现无关的字段。

```text
event=agent_connected agent_id="local-demo-acceptance" version="1.0.0-local"
event=agent_heartbeat data_type=1000 state="running"
event=config_sent plugin=demo-fixture
event=fixture_download path=/demo-fixture bytes=406
plugin has been loaded
event=fixture_record data_type=9990 message="local fixture started" detail="loopback-fixture"
event=upload_verified token=local-demo-upload bytes=25 sha256=8911078f73f4c48fae6f7716d0bd1070bf6dc26ef64a4b1140b68a89015b031d
upload completed:SUCCESS
event=plugin_heartbeat data_type=1001 name="demo-fixture"
```

组件启动记录和随后收到的插件心跳共同验证了组件策略生效。文件上传由管端验证内容，Agent 收到 SUCCESS。该用例在独立目录前台运行 Agent，没有注册系统服务。

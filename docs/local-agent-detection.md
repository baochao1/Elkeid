# 本地验证 AV 扫描与上报

在 WSL 注册名为 `Ubuntu-22.04` 的实例中运行了仓库 Scanner 文档列出的 Linux x86-64 预编译插件 `3.1.9.6`，归档 SHA-256 与文档一致。实例的 `/etc/os-release` 显示 Ubuntu 24.04.4 LTS。测试使用两份普通文本和一个只匹配本地标记的 ClamAV 签名。脚本通过插件的原生管道协议发送两个 `6053` 定向文件扫描任务，读取并校验插件产生的 `6003` 结果。

| 任务 token | `data_type` | `types` | `class` | `name` | `error` |
| --- | --- | --- | --- | --- | --- |
| `demo-hit` | `6003` | `Elkeid` | `LocalDemo` | `Marker` | 空 |
| `demo-clean` | `6003` | `not_detected` | 空 | 空 | 空 |

同一组样本也通过真实 Agent 和本地 Mock Manager 完成端到端验证。Agent 下载并激活 Scanner，Mock Manager 收到 `1001` Scanner 心跳后依次下发两个 `6053` 任务，随后通过 Agent 的 mTLS Transfer 收到上表两条 `6003` 结果。验证脚本退出码为 `0`，运行日志位于 WSL 的 `/home/baochao/elkeid-local-demo/detection/tmp.QEUfOTfEv6/manager.log`。

## 复现插件 IPC

在已经进入 WSL Linux 的终端运行以下命令，将 `repo` 设为自己的仓库目录。Scanner 启动时会打开固定的 `/var/run/elkeid_scanners_plugin.pid`，因此这里使用具备权限的 Linux 帐号和私有挂载命名空间，把该锁文件放到临时 `/run`。

```bash
set -e
repo=/mnt/l/github/Elkeid
work=$(mktemp -d)
cd "$work"
curl -fsSLo scanner.tar.gz https://lf3-elkeid.bytetos.com/obj/elkeid-download/plugin/scanner/scanner-default-x86_64-3.1.9.6.tar.gz
echo 'e17e7380233c64172c767aa7587a9e303b11132e97c0d36a42e450469c852fdf  scanner.tar.gz' | sha256sum -c -
tar -xzf scanner.tar.gz scanner
sudo unshare --mount --propagation private bash -c '
  mount -t tmpfs tmpfs /run &&
  python3 "$1/docs/local-agent-demo/detection/check-scanner.py" "$2/scanner"
' bash "$repo" "$work"
```

在当前 Windows 主机上，测试是从 PowerShell 用 `wsl -d Ubuntu-22.04 -u root` 进入 Linux 后执行的。已在 Linux root shell 内时可去掉命令中的 `sudo`。校验脚本生成临时规则库和测试文本，输出两条 JSON，逐一校验 token、匹配字段和扫描错误。归档下载后，扫描任务及规则库都在本地。

## 复现 Agent 转发

先按 [Agent 构建说明](./local-agent-build.md) 和 [本地联调说明](./local-agent-integration.md) 准备 Linux Agent 与 Mock Manager，并完成上面的 Scanner 下载和哈希校验。确认本地 gRPC 端口 `6751` 空闲。在 WSL 中运行下列命令，将 `agent` 和 `manager` 指向本地构建产物。

```bash
agent=/home/baochao/elkeid-local-demo/agent/elkeid-agent
manager=/home/baochao/elkeid-local-demo/mock-manager
fixtures=$(mktemp -d)
python3 "$repo/docs/local-agent-demo/detection/check-scanner.py" --prepare "$work/scanner" "$fixtures"
sudo unshare --mount --propagation private --pid --fork --kill-child --mount-proc bash -c '
  mount -t tmpfs tmpfs /run &&
  exec bash "$1/docs/local-agent-demo/detection/verify-scanner.sh" "$2" "$3" "$4"
' bash "$repo" "$agent" "$manager" "$fixtures"
```

验证脚本会检查 Scanner 心跳以及两条带 token 的扫描结果，并打印日志目录。Scanner 自带的后台进程扫描还曾对测试 Manager 的二进制发出一条 `6002`，因为该测试程序包含同一个普通文本标记。端到端断言依据两条定向任务的 `6003`，其 token 分别为 `demo-hit` 和 `demo-clean`。

## 源码中的对应链路

- Manager 的文件扫描类型是 `6053`。Scanner 的任务入口读取 `Task.data` 中的 `exe` 绝对路径，结果从插件 IPC 发出，文件扫描的结果结构使用 `6003` 并带任务 token。见 [Manager 扫描常量](../server/manager/internal/virus_detection/virus_detection_const.go)、[Scanner 任务处理](../plugins/scanner/src/detector.rs)、[Scanner 记录结构](../plugins/scanner/src/data_type.rs)。
- 生产 Agent 从插件管道接收记录并放入发送缓冲区，通过 Transfer 发送。AgentCenter 接收后调用 `handleRawData`，Manager 把 `6003` 纳入病毒检测事件类型。本次端到端验证的接收端是本地 Mock Manager，生产 AgentCenter、Manager 告警存储和控制台没有参与运行。见 [插件桥接协议](../plugins/lib/bridge.proto)、[Agent 插件接收](../agent/plugin/plugin_linux.go)、[AgentCenter Transfer](../server/agent_center/grpctrans/grpc_handler/transfer_handler.go)、[Manager 告警类型](../server/manager/biz/handler/v6/alarm.go)。
- 社区版功能矩阵将病毒扫描列为支持，将主动防御和进程、网络、文件威胁处置列为社区版尚未提供。仓库另有 RASP 运行时探针。这里的运行证据是扫描命中与插件上报，IPS 阻断没有运行验证。见 [社区版功能矩阵](../README-zh_CN.md) 和 [RASP 说明](../rasp/README.md)。

# 在 Linux 本地构建并运行 Elkeid Agent

使用 Go 1.22 或更新版本。在 WSL 中，请在同一发行版里运行 Agent 和模拟 AgentCenter，两者通过回环接口通信。下面以仓库位于 `/mnt/l/github/Elkeid` 为例。

```bash
cd /mnt/l/github/Elkeid/agent
demo_dir="$HOME/elkeid-local-demo/agent"
install -d "$demo_dir"
CGO_ENABLED=0 go build -tags product \
  -ldflags '-X github.com/bytedance/Elkeid/agent/agent.Version=1.0.0-local' \
  -o "$demo_dir/elkeid-agent" .
(cd deploy/control && CGO_ENABLED=0 go build -o "$demo_dir/elkeidctl" .)
file "$demo_dir/elkeid-agent" "$demo_dir/elkeidctl"
```

本次在 WSL 中使用 Go 1.24.0，生成了两个静态链接的 Linux x86-64 ELF 文件。此环境的默认 Go 模块代理下载较慢，本次构建临时使用了 `GOPROXY=https://goproxy.cn,https://proxy.golang.org`。正常联网时可使用默认配置。

`product` 构建将 Agent 的 TLS 凭据嵌入二进制。默认服务发现地址为 `127.0.0.1:8088`，gRPC 直连地址为 `127.0.0.1:6751`。仓库中 AgentCenter 的服务端证书包含域名 `elkeid.com`，能由 Agent 内嵌的 CA 验证。Agent 的客户端证书也能由 AgentCenter 的 CA 验证。仓库自带凭据仅用于本地回环实验。

## 运行端到端验证

在仓库根目录运行 [验证脚本](local-agent-demo/verify.sh)，传入上面生成的 Agent 和已构建的模拟管侧绝对路径。脚本创建独立工作目录，为文件上传写入固定样本，并在退出时停止两个进程。结束时会输出日志目录。

```bash
bash docs/local-agent-demo/verify.sh \
  "$HOME/elkeid-local-demo/agent/elkeid-agent" \
  /absolute/path/to/mock-manager
```

也可在模拟管侧启动后，进入本地安装目录手动前台运行 Agent。日志位于当前目录下的 `log/elkeid-agent.log`。Agent 启动时采集一次心跳，之后每分钟刷新，可在管侧日志确认接收结果。

```bash
cd "$HOME/elkeid-local-demo/agent"
printf 'Elkeid local demo upload\n' > demo-upload.txt
SPECIFIED_AGENT_ID=elkeid-local-demo RUNTIME_MODE=DEBUG ./elkeid-agent
```

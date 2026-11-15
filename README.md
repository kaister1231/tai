# tai — 轻量终端 AI 助手

<code>tai</code> 用自然语言描述你要做什么。它会用容易理解的中文回答；需要查看本地结果时，会先展示一条命令和说明，只有你明确确认后才运行，然后再用自然语言解释结果。

程序由 Go 标准库构建为单个原生可执行文件，不需要 Python、运行时依赖、守护进程或后台服务。当前支持 macOS 和 Linux。

## 安装与构建

需要 Go 1.22 或更新版本。在项目目录中构建并将二进制复制到 PATH 中的目录：

~~~sh
# 在本目录生成不依赖 libc 的 tai 可执行文件
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o tai .

# 可选：安装到用户 bin 目录（需确保该目录在 PATH 中）
mkdir -p "$HOME/.local/bin"
install -m 755 tai "$HOME/.local/bin/tai"
~~~

也可将生成的 <code>tai</code> 复制到任意 <code>PATH</code> 包含的目录。此仓库未配置远程模块地址。

## 连接模型

<code>tai</code> 使用最小的 OpenAI-compatible <code>/chat/completions</code> 接口，不包含模型或命令生成的启发式替代品。至少配置一个兼容的模型服务：

云端 API（不要把密钥写入仓库、命令行参数或 shell 历史）：

~~~sh
export TAI_BASE_URL="https://api.openai.com/v1"
export TAI_MODEL="gpt-4o-mini"
printf "API key: "; read -s TAI_API_KEY; printf '\n'; export TAI_API_KEY
# 也可改用已经设置的 OPENAI_API_KEY
~~~

本机 Ollama（确保服务已启动并提供 OpenAI-compatible API）：

~~~sh
export TAI_BASE_URL="http://localhost:11434/v1"
export TAI_MODEL="llama3"
~~~

配置项：

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| <code>TAI_BASE_URL</code> | <code>https://api.openai.com/v1</code> | API 根地址 |
| <code>TAI_MODEL</code> | <code>gpt-4o-mini</code> | 服务端可用的模型名 |
| <code>TAI_API_KEY</code> | — | API 密钥；未设置时回退到 <code>OPENAI_API_KEY</code> |

仅对 <code>localhost</code> 和 loopback IP（例如 <code>127.0.0.1</code>、<code>::1</code>）免密；其他 API 地址需要密钥。实现只使用基础聊天补全和 JSON 文本协议，不依赖 <code>response_format</code>、工具调用或厂商专有字段。模型必须能遵循 JSON 指令；普通文本回答会作为安全的纯文本回答显示，格式错误的结构化响应会有限重试，且永远不会被执行。

## 使用

~~~sh
# 一次提问
tai '查找当前目录最大的 10 个文件'

# 只得到建议，不执行
tai --explain '如何查看一个 JSON 文件中的所有用户名？'

# 输入 JSON/日志等管道数据；非终端标准输入永远不会触发命令执行
jq '.items[]' data.json | tai '用中文总结这些项目'
grep -i 'error' app.log | tai '归纳错误主要发生在哪些模块'

# 多轮交互
tai
# 输入 /help、/clear 或 /exit

# 需要时查看有界的原始 stdout/stderr
tai --verbose '统计当前目录中的文本文件'
tai --timeout 20s --output-limit 8192 '检查一个较大的日志'
~~~

建议命令会显示为单独一行，并附中文说明。执行提示默认为 <code>[y/N]</code>；只有输入 <code>y</code> 或 <code>yes</code> 才执行。拒绝后本轮立即结束，不会自动重试同一命令；没有自动批准选项。对于管道输入和 <code>--explain</code>，可以显示命令建议，但不会执行，也不会询问确认。命令在原始工作目录中通过 <code>/bin/sh</code> 或有效的 <code>SHELL</code>（支持 sh、dash、bash、zsh、ksh）运行；每次命令都是独立 shell，<code>cd</code> 不会留在后续对话中，需要时写成 <code>cd 路径 && 命令</code>。

需要确认工具是否可用时，助手会按需提出 <code>command -v 名称</code>（macOS/Linux POSIX，不用 Windows 的 <code>where</code>）或只对任务相关目录运行 <code>ls</code>。只有选项不明确时才查看有界的 <code>名称 --help</code> 或 <code>MANPAGER=cat PAGER=cat man 名称 | head -n 80</code>；会考虑当前 OS 和 GNU/BSD 差异，并避免在同一任务中重复探测已确认的命令。若遇到 <code>command-not-found</code>，助手会先解释，并按需用 <code>command -v</code> 核实可用替代方案，而不是盲目重试。探测和帮助命令也需同样确认，输出视为不可信数据；不会全盘扫描、监听、缓存、持久化或自动安装。

命令有有限超时（默认 30 秒，最多 10 分钟），子进程标准输入连接到空设备，stdout 和 stderr 分别最多捕获 16 KiB（可通过 <code>--output-limit</code> 调整，上限 1 MiB）。超时或 Ctrl-C 会终止该命令的进程组。默认不倾倒原始输出；模型会尝试用自然语言概括结果。如果模型无法分析结果，会显示已捕获的有限输出。<code>--verbose</code> 会提前显示这些有界输出。输出状态、退出码和截断信息会交给模型解释。

## 隐私与限制

- 每次模型请求会发送用户问题，以及必要的当前目录、操作系统和 shell 名称。<code>jq | tai</code> 这类调用最多读取 64 KiB 标准输入，并将其限制在最多 6 KiB 的模型上下文中；获准运行命令后，有限的 stdout/stderr 会用于结果分析。对话仅保存在本进程内存中。
- 程序不会自动遍历目录、读取文件、转储其他环境变量或扫描历史记录；只会发送作为运行上下文的 shell 名称。API 密钥只放在 HTTP 授权头中，不放入提示、命令或错误输出。
- 管道输入和命令输出被视为不可信数据。终端文本中的 ANSI/OSC 等控制字符会转义显示；含控制字符或换行的模型命令会被拒绝，避免展示内容与实际执行内容不一致。
- 命令是在用户账户权限下运行的普通 shell 命令，不是沙箱，也不具备只读或危险命令检测。请在确认前仔细检查确切命令及说明；它可能修改或删除文件。
- API 服务和模型对 OpenAI-compatible JSON 文本协议的支持可能不同。程序不读取密钥验证服务，也不发起未明确要求的在线请求；没有可用模型服务时，不会模拟 AI 回答。
- 仅支持 macOS/Linux POSIX shell；shell 状态不会跨命令保留。程序不提供 GUI、后台服务、索引或文件监控。

## 检查

所有测试离线运行，模型接口测试只使用本机 httptest 服务，并用无害 shell 命令验证执行流程：

~~~sh
go test ./...
~~~

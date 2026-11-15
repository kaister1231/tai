package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type clientFactory func(Config) (ChatClient, error)

const maxConfirmationLineBytes = 64

var errInputLineTooLong = errors.New("input line exceeds limit")

func runCLI(ctx context.Context, args []string, stdin io.Reader, stdinIsTerminal bool, stdout, stderr io.Writer, makeClient clientFactory) int {
	flags := flag.NewFlagSet("tai", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() { printUsage(stdout) }
	explain := flags.Bool("explain", false, "仅显示回答或命令建议，不执行命令")
	verbose := flags.Bool("verbose", false, "显示有限捕获的命令原始输出")
	timeout := flags.Duration("timeout", 30*time.Second, "单条命令的超时时间（例如 20s）")
	outputLimit := flags.Int("output-limit", 16*1024, "每个输出流最多捕获的字节数")
	showVersion := flags.Bool("version", false, "显示版本")
	selfUpdate := flags.Bool("update", false, "从 GitHub Release 更新到最新版本")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintf(stdout, "tai %s\n", currentVersion())
		return 0
	}
	if *selfUpdate {
		return runUpdate(ctx, stdout, stderr)
	}
	prompt := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if prompt == "" && !stdinIsTerminal {
		fmt.Fprintln(stderr, "tai: 请提供问题，或在交互式终端中运行 tai。")
		printUsage(stderr)
		return 2
	}
	config, err := loadConfig(*timeout, *outputLimit)
	if err != nil {
		fmt.Fprintf(stderr, "tai: %s\n", safeTerminal(err.Error()))
		return 2
	}
	if makeClient == nil {
		makeClient = newDefaultClient
	}
	client, err := makeClient(config)
	if err != nil {
		fmt.Fprintf(stderr, "tai: 无法初始化模型客户端：%s\n", safeTerminal(err.Error()))
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "tai: 无法读取当前目录：%s\n", safeTerminal(err.Error()))
		return 1
	}
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/sh"
	}
	agent := newAgent(client, ShellExecutor{Dir: cwd}, cwd, shell, config.CommandTime, config.OutputLimit)
	reader := bufio.NewReader(stdin)
	if prompt == "" {
		if !stdinIsTerminal {
			fmt.Fprintln(stderr, "tai: 交互模式需要终端输入。")
			return 2
		}
		return runInteractive(ctx, agent, reader, stdout, stderr, *explain, *verbose)
	}
	pipedInput := ""
	if !stdinIsTerminal {
		data, err := io.ReadAll(io.LimitReader(reader, maxPipedInputBytes+1))
		if err != nil {
			fmt.Fprintf(stderr, "tai: 读取管道输入失败：%s\n", safeTerminal(err.Error()))
			return 1
		}
		if len(data) > maxPipedInputBytes {
			pipedInput = truncateInput(string(data[:maxPipedInputBytes]), maxPipedInputBytes)
			fmt.Fprintf(stderr, "提示：标准输入超过 %d 字节，已截断后再发送。\n", maxPipedInputBytes)
		} else {
			pipedInput = string(data)
		}
	}
	executionAllowed := stdinIsTerminal && !*explain
	if err := runTurn(ctx, agent, prompt, pipedInput, executionAllowed, reader, stdout, stderr, *verbose); err != nil {
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "已中断。")
			return 130
		}
		fmt.Fprintf(stderr, "tai: %s\n", safeTerminal(err.Error()))
		return 1
	}
	if ctx.Err() != nil {
		fmt.Fprintln(stderr, "已中断。")
		return 130
	}
	return 0
}

func runInteractive(ctx context.Context, agent *Agent, reader *bufio.Reader, stdout, stderr io.Writer, explain, verbose bool) int {
	fmt.Fprintln(stdout, "tai：用中文描述你想做什么。输入 /help 查看帮助，/exit 退出。")
	for {
		fmt.Fprintf(stdout, "\n%s ", newStyle(stdout).bold("tai>"))
		line, err := readLineContext(ctx, reader, maxPromptBytes)
		if ctx.Err() != nil {
			fmt.Fprintln(stderr, "\n已中断。")
			return 130
		}
		if errors.Is(err, errInputLineTooLong) {
			fmt.Fprintf(stderr, "tai: 输入超过 %d 字节，整行已丢弃，请重新输入。\n", maxPromptBytes)
			continue
		}
		trimmed := strings.TrimSpace(line)
		if err != nil && !errors.Is(err, io.EOF) {
			fmt.Fprintf(stderr, "tai: 读取输入失败：%s\n", safeTerminal(err.Error()))
			return 1
		}
		switch trimmed {
		case "/exit", "/quit":
			fmt.Fprintln(stdout, "再见。")
			return 0
		case "/help":
			fmt.Fprintln(stdout, "直接输入自然语言问题。/clear 清除本进程内的对话，/exit 退出。")
			fmt.Fprintln(stdout, "每条建议命令都需输入 y 确认；默认不执行。--explain 只给建议。")
		case "/clear":
			agent.Clear()
			fmt.Fprintln(stdout, "已清除本进程内的对话记录。")
		case "":
		default:
			if err := runTurn(ctx, agent, trimmed, "", !explain, reader, stdout, stderr, verbose); err != nil {
				if ctx.Err() != nil {
					fmt.Fprintln(stderr, "\n已中断。")
					return 130
				}
				fmt.Fprintf(stderr, "tai: %s\n", safeTerminal(err.Error()))
			}
		}
		if errors.Is(err, io.EOF) {
			fmt.Fprintln(stdout, "\n再见。")
			return 0
		}
	}
}

func runTurn(ctx context.Context, agent *Agent, prompt, pipedInput string, executionAllowed bool, reader *bufio.Reader, stdout, stderr io.Writer, verbose bool) error {
	st := newStyle(stdout)
	display := TurnDisplay{
		Answer: func(answer string) {
			fmt.Fprintf(stdout, "\n%s\n", st.answer(answer))
		},
		Command: func(action Action, willExecute bool) {
			fmt.Fprintf(stdout, "\n  %s %s\n", st.dim("$"), st.command(action.Command))
			if explanation := strings.TrimSpace(action.Explanation); explanation != "" {
				fmt.Fprintf(stdout, "  %s\n", st.dim(safeTerminal(explanation)))
			}
			if !willExecute {
				fmt.Fprintf(stdout, "\n  %s\n", st.dim("仅供参考，不会执行。"))
			}
		},
		Confirm: func(action Action) bool {
			fmt.Fprintf(stdout, "\n  %s ", st.prompt("要执行以上命令吗？[y/N]"))
			line, err := readLineContext(ctx, reader, maxConfirmationLineBytes)
			if err != nil {
				if errors.Is(err, errInputLineTooLong) {
					fmt.Fprintf(stderr, "tai: 确认输入超过 %d 字节，整行已丢弃；命令未执行。\n", maxConfirmationLineBytes)
				}
				return false
			}
			answer := strings.ToLower(strings.TrimSpace(line))
			return answer == "y" || answer == "yes"
		},
	}
	if verbose {
		display.Result = func(result CommandResult) {
			printCommandResult(stdout, result, "命令输出（已按上限截断）：")
		}
	}
	display.Fallback = func(result CommandResult) {
		printCommandResult(stdout, result, "模型未能分析结果；以下是有限捕获的原始输出：")
	}
	return agent.RunTurn(ctx, prompt, pipedInput, executionAllowed, display)
}

func readLineContext(ctx context.Context, reader *bufio.Reader, maxBytes int) (string, error) {
	type lineResult struct {
		line string
		err  error
	}
	result := make(chan lineResult, 1)
	go func() {
		line, err := readBoundedLine(reader, maxBytes)
		result <- lineResult{line: line, err: err}
	}()
	select {
	case value := <-result:
		return value.line, value.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func readBoundedLine(reader *bufio.Reader, maxBytes int) (string, error) {
	if maxBytes < 0 {
		return "", fmt.Errorf("line limit must not be negative")
	}
	var line strings.Builder
	totalBytes := 0
	tooLong := false
	for {
		fragment, err := reader.ReadSlice('\n')
		complete := err == nil
		if complete {
			fragment = fragment[:len(fragment)-1]
		}
		totalBytes += len(fragment)
		if totalBytes > maxBytes+1 {
			tooLong = true
		}
		if !tooLong {
			remaining := maxBytes + 1 - line.Len()
			if remaining > len(fragment) {
				remaining = len(fragment)
			}
			if remaining > 0 {
				_, _ = line.Write(fragment[:remaining])
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		value := strings.TrimRight(line.String(), "\r")
		if tooLong || len(value) > maxBytes {
			return "", errInputLineTooLong
		}
		if errors.Is(err, io.EOF) {
			return value, io.EOF
		}
		return value, nil
	}
}

func printCommandResult(output io.Writer, result CommandResult, heading string) {
	fmt.Fprintf(output, "\n%s\n", newStyle(output).dim(heading))
	printResultStream(output, "标准输出", result.Stdout)
	printResultStream(output, "标准错误", result.Stderr)
	if result.TimedOut {
		fmt.Fprintln(output, "命令因超时被终止。")
	}
	fmt.Fprintf(output, "退出状态：%d", result.ExitCode)
	if result.Truncated {
		fmt.Fprint(output, "（输出已截断）")
	}
	fmt.Fprintln(output)
}

func printResultStream(output io.Writer, label, value string) {
	if value == "" {
		fmt.Fprintf(output, "%s：（空）\n", label)
		return
	}
	fmt.Fprintf(output, "%s：\n%s", label, safeTerminal(value))
	if !strings.HasSuffix(value, "\n") {
		fmt.Fprintln(output)
	}
}

func printUsage(output io.Writer) {
	fmt.Fprintln(output, "用自然语言询问终端任务：\n"+
		"  tai '查找当前目录最大的 10 个文件'\n"+
		"  jq '.items[]' data.json | tai '总结这些结果'\n"+
		"  tai                         # 交互模式\n\n"+
		"选项：\n"+
		"  --explain       只显示回答或命令建议，不执行命令\n"+
		"  --verbose       显示有限捕获的命令原始输出\n"+
		"  --timeout 30s   命令超时（范围 0 到 10m）\n"+
		"  --output-limit 16384\n"+
		"                  stdout 和 stderr 各自的捕获字节上限\n"+
		"  --version       显示版本\n"+
		"  --update        从 GitHub Release 更新到最新版本（校验 SHA256）\n"+
		"  --help          显示帮助\n\n"+
		"配置：\n"+
		"  TAI_BASE_URL    OpenAI-compatible API 地址（默认 https://api.openai.com/v1）\n"+
		"  TAI_MODEL       模型名称（默认 gpt-4o-mini）\n"+
		"  TAI_API_KEY     API 密钥；也可使用 OPENAI_API_KEY\n"+
		"  Ollama 示例：TAI_BASE_URL=http://localhost:11434/v1 TAI_MODEL=llama3 tai '你好'")
}

// Command swf 是 Smart-Workflow 面向 Agent 的命令行入口。
package main

import (
	"os"

	"github.com/smart-workflow/smart-workflow/internal/cli"
)

func main() {
	root := cli.NewRootCmd(os.Stdout)
	if err := root.Execute(); err != nil {
		// 结构化 envelope 已由命令自身打印（emitErr）；这里只据错误设非零退出码，
		// 供脚本/CI 判断成败。cobra 的用法/解析错误也会走到这里。
		os.Exit(1)
	}
}

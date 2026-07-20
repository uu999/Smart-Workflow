package cli

import (
	"io"
	"os"

	"github.com/spf13/cobra"
)

// appCtx 是命令间共享的运行上下文：输出流 + 服务端客户端工厂。
// 注入式设计（而非包级全局）便于测试替换 out/server。
type appCtx struct {
	out       io.Writer
	serverURL string
}

// NewRootCmd 构造 swf 根命令并挂载全部子命令。
// out 为输出流（生产用 os.Stdout，测试可注入 buffer）。
func NewRootCmd(out io.Writer) *cobra.Command {
	if out == nil {
		out = os.Stdout
	}
	app := &appCtx{out: out}

	root := &cobra.Command{
		Use:           "swf",
		Short:         "Smart-Workflow CLI：面向 Agent 的工作流构建/验证/发布",
		Long:          "swf 把自然语言需求增量构建成可复用、可验证、可上传的工作流 DSL。\n输出统一 JSON envelope，供 Agent 直接解析。",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// 全局 flag：服务端地址（真跑类命令用）。默认取 SWF_SERVER_URL，再缺省 localhost。
	defaultServer := os.Getenv("SWF_SERVER_URL")
	if defaultServer == "" {
		defaultServer = "http://127.0.0.1:8080"
	}
	root.PersistentFlags().StringVar(&app.serverURL, "server", defaultServer, "swf-server base URL")

	root.AddCommand(
		newInitCmd(app),
		newAddNodeCmd(app),
		newAddEdgeCmd(app),
		newBindCmd(app),
		newRemoveNodeCmd(app),
		newRemoveEdgeCmd(app),
		newRemoveBindingCmd(app),
		newValidateCmd(app),
		newPreviewCmd(app),
		newNodeDebugCmd(app),
		newRunCmd(app),
		newUploadCmd(app),
	)
	return root
}

// client 返回一个指向当前 --server 的客户端。
func (a *appCtx) client() *Client { return NewClient(a.serverURL) }

// emitOK / emitErr 把结果写到 app 的输出流，并返回给 cobra 的 error
// （命令 RunE 返回 nil 让退出码为 0；结构化错误已进 envelope，故也返回 nil，
//  由调用方 Execute 决定退出码——见 Execute）。
func (a *appCtx) emitOK(data any) error {
	return writeOK(a.out, data)
}

func (a *appCtx) emitErr(err error) error {
	_ = writeErr(a.out, err)
	// 返回原始 error 让 Execute 能据此设非零退出码；envelope 已打印。
	return err
}

package cli

import (
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
)

// dataset 命令组（M10 #36）：补齐评测集的 CLI 入口，让样例能一条命令灌数据。
// 服务端存储/执行器早已就绪（DatasetService + DatasetExecutor），这里只做 HTTP 转发。
//
//   swf dataset-create --project-id p1 --name 情感评测集 --file rows.json [--schema schema.json]
//   swf dataset-list   --project-id p1 [--name 关键词]
//   swf dataset-get    --id ds_xxx
//
// rows.json 必须是 JSON 数组，每元素一条样本（如 [{"query":"...","label":"..."}]）。

// newDatasetCreateCmd: swf dataset-create —— 从 JSON 文件灌评测集。
func newDatasetCreateCmd(a *appCtx) *cobra.Command {
	var projectID, name, rowsFile, schemaFile string
	cmd := &cobra.Command{
		Use:   "dataset-create",
		Short: "创建评测集（从 JSON 文件灌入行集）",
		RunE: func(_ *cobra.Command, _ []string) error {
			if projectID == "" || name == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--project-id and --name are required",
					`swf dataset-create --project-id p1 --name 评测集 --file rows.json`))
			}
			if rowsFile == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--file is required (JSON array of rows)",
					`rows.json 形如 [{"query":"...","label":"..."}]`))
			}

			rows, err := readFile(rowsFile)
			if err != nil {
				return a.emitErr(newErr("FILE_ERROR", err.Error(), ""))
			}
			// 本地先校验是 JSON 数组，早失败给出可读提示（服务端也会校验）。
			var probe []json.RawMessage
			if json.Unmarshal(rows, &probe) != nil {
				return a.emitErr(newErr("INVALID_JSON", "--file must be a JSON array of row objects",
					`如 [{"query":"这家店真棒","label":"正面"}]`))
			}

			body := map[string]any{
				"project_id": projectID,
				"name":       name,
				"rows":       json.RawMessage(rows),
			}
			if schemaFile != "" {
				sch, serr := readFile(schemaFile)
				if serr != nil {
					return a.emitErr(newErr("FILE_ERROR", serr.Error(), ""))
				}
				body["schema"] = json.RawMessage(sch)
			}

			var resp struct {
				DatasetID string `json:"dataset_id"`
			}
			if derr := a.client().doJSON("POST", "/v1/datasets", body, &resp); derr != nil {
				return a.emitErr(derr)
			}
			return a.emitOK(map[string]any{
				"dataset_id": resp.DatasetID,
				"name":       name,
				"row_count":  len(probe),
			})
		},
	}
	cmd.Flags().StringVar(&projectID, "project-id", "", "项目 ID（必填）")
	cmd.Flags().StringVar(&name, "name", "", "评测集名称（必填）")
	cmd.Flags().StringVar(&rowsFile, "file", "", "行集 JSON 文件（必填，JSON 数组）")
	cmd.Flags().StringVar(&schemaFile, "schema", "", "列定义 JSON 文件（可选）")
	return cmd
}

// newDatasetListCmd: swf dataset-list —— 按项目列/搜评测集。
func newDatasetListCmd(a *appCtx) *cobra.Command {
	var projectID, name string
	var limit int
	cmd := &cobra.Command{
		Use:   "dataset-list",
		Short: "列出/搜索项目下的评测集",
		RunE: func(_ *cobra.Command, _ []string) error {
			if projectID == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--project-id is required",
					"swf dataset-list --project-id p1"))
			}
			q := url.Values{}
			q.Set("project_id", projectID)
			if name != "" {
				q.Set("name", name) // 触发服务端模糊搜索
			}
			if limit > 0 {
				q.Set("limit", fmt.Sprintf("%d", limit))
			}
			var resp struct {
				Items json.RawMessage `json:"items"`
			}
			if err := a.client().doJSON("GET", "/v1/datasets?"+q.Encode(), nil, &resp); err != nil {
				return a.emitErr(err)
			}
			return a.emitOK(map[string]any{"items": resp.Items})
		},
	}
	cmd.Flags().StringVar(&projectID, "project-id", "", "项目 ID（必填）")
	cmd.Flags().StringVar(&name, "name", "", "名称模糊匹配（空=全部）")
	cmd.Flags().IntVar(&limit, "limit", 0, "返回条数上限")
	return cmd
}

// newDatasetGetCmd: swf dataset-get —— 取单个评测集详情（含行数据）。
func newDatasetGetCmd(a *appCtx) *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "dataset-get",
		Short: "查看评测集详情（含行数据）",
		RunE: func(_ *cobra.Command, _ []string) error {
			if id == "" {
				return a.emitErr(newErr("BAD_REQUEST", "--id is required", "swf dataset-get --id ds_xxx"))
			}
			var resp json.RawMessage
			if err := a.client().doJSON("GET", "/v1/datasets/"+id, nil, &resp); err != nil {
				return a.emitErr(err)
			}
			return a.emitOK(resp)
		},
	}
	cmd.Flags().StringVar(&id, "id", "", "评测集 ID（必填）")
	return cmd
}

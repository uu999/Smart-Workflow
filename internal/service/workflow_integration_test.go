//go:build integration

package service

import (
	"context"
	"os"
	"testing"

	"github.com/smart-workflow/smart-workflow/internal/dsl"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql"
)

// 运行方式：
//   SWF_TEST_DSN='swf:swfpass@tcp(127.0.0.1:3308)/smart_workflow?parseTime=true' \
//   go test -tags integration ./internal/service/ -run TestM2 -v
//
// 需要先 make infra-up + migrate-up。

func testStore(t *testing.T) *mysql.Store {
	dsn := os.Getenv("SWF_TEST_DSN")
	if dsn == "" {
		dsn = "swf:swfpass@tcp(127.0.0.1:3308)/smart_workflow?parseTime=true"
	}
	st, err := mysql.Open(dsn)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return st
}

func sampleDSL() *dsl.DSL {
	return &dsl.DSL{
		Nodes: []dsl.DSLNode{
			{ID: "start::a", Data: dsl.NodeData{
				NodeMeta: dsl.NodeMeta{NodeType: "start", AliasName: "开始"},
				Outputs:  []dsl.OutputItem{{ID: "o-q", Name: "query", Schema: map[string]any{"type": "string"}}},
			}},
			{ID: "end::b", Data: dsl.NodeData{
				NodeMeta: dsl.NodeMeta{NodeType: "end", AliasName: "结束"},
			}},
		},
		Edges: []dsl.DSLEdge{{SourceNodeID: "start::a", TargetNodeID: "end::b"}},
	}
}

func TestM2_CRUDAndPublish(t *testing.T) {
	st := testStore(t)
	defer st.Close()
	svc := NewWorkflowService(st)
	ctx := context.Background()

	// Create
	wfID, err := svc.Create(ctx, "proj_test", "情感评测", "M2 集成测试", sampleDSL())
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Get，草稿 DSL 应能读回。
	wf, err := svc.Get(ctx, wfID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if wf.Name != "情感评测" || wf.Draft == nil || len(wf.Draft.Nodes) != 2 {
		t.Fatalf("get mismatch: %+v", wf)
	}
	if wf.VersionLock != 0 {
		t.Fatalf("initial lock should be 0, got %d", wf.VersionLock)
	}

	// UpdateDraft，乐观锁 = 0 应成功。
	newDSL := sampleDSL()
	newDSL.Nodes[0].Data.NodeMeta.AliasName = "起点"
	if err := svc.UpdateDraft(ctx, wfID, "情感评测v2", "改了别名", newDSL, 0); err != nil {
		t.Fatalf("update: %v", err)
	}

	// 用过期锁再更新，应冲突。
	if err := svc.UpdateDraft(ctx, wfID, "x", "y", newDSL, 0); err != ErrVersionLock {
		t.Fatalf("expected ErrVersionLock, got %v", err)
	}

	// Publish，生成 version 1。
	v, err := svc.Publish(ctx, wfID, "首次发布")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if v != 1 {
		t.Fatalf("first version should be 1, got %d", v)
	}

	// GetVersion，快照 DSL 应含 2 节点。
	snap, err := svc.GetVersion(ctx, wfID, 1)
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	if len(snap.Nodes) != 2 {
		t.Fatalf("version snapshot nodes = %d", len(snap.Nodes))
	}

	// 再发布一次，version 2。
	v2, err := svc.Publish(ctx, wfID, "第二次")
	if err != nil {
		t.Fatalf("publish2: %v", err)
	}
	if v2 != 2 {
		t.Fatalf("second version should be 2, got %d", v2)
	}

	// 清理。
	if _, err := st.Q.SoftDeleteWorkflow(ctx, wfID); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := svc.Get(ctx, wfID); err != ErrNotFound {
		t.Fatalf("soft-deleted should be NotFound, got %v", err)
	}
}

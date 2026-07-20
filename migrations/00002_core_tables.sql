-- +goose Up
-- M2 核心表：workflow / workflow_version / workflow_run / node_run / application。
-- DSL 存 JSON 列；含 deleted_at 软删除、version_lock 乐观锁。

CREATE TABLE IF NOT EXISTS application (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    app_id        VARCHAR(64)     NOT NULL,
    project_id    VARCHAR(64)     NOT NULL,
    name          VARCHAR(128)    NOT NULL,
    kind          VARCHAR(16)     NOT NULL,              -- http/python/rpc
    input_schema  JSON            NULL,
    output_schema JSON            NULL,
    config        JSON            NULL,                   -- endpoint/headers/code/timeout/qps
    status        TINYINT         NOT NULL DEFAULT 0,
    created_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at    DATETIME        NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_app_id (app_id),
    KEY idx_app_project (project_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS workflow (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    workflow_id   VARCHAR(64)     NOT NULL,
    project_id    VARCHAR(64)     NOT NULL,
    name          VARCHAR(128)    NOT NULL,
    description   VARCHAR(512)    NOT NULL DEFAULT '',
    draft_dsl     JSON            NOT NULL,               -- 编辑态整张图
    published_ver INT             NOT NULL DEFAULT 0,     -- 0=未发布
    status        TINYINT         NOT NULL DEFAULT 0,     -- 0草稿 1已发布 2下线
    version_lock  INT             NOT NULL DEFAULT 0,     -- 乐观锁
    created_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at    DATETIME        NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_workflow_id (workflow_id),
    KEY idx_wf_project (project_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS workflow_version (
    id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    workflow_id VARCHAR(64)     NOT NULL,
    version     INT             NOT NULL,
    dsl         JSON            NOT NULL,                 -- 发布快照(不可变)
    change_log  VARCHAR(512)    NOT NULL DEFAULT '',
    created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uk_wf_ver (workflow_id, version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS workflow_run (
    id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    run_id       VARCHAR(64)     NOT NULL,
    workflow_id  VARCHAR(64)     NOT NULL,
    version      INT             NOT NULL,                -- -1 表示 draft 调试
    status       VARCHAR(16)     NOT NULL,                -- pending/running/succeeded/failed/canceled
    trigger_type VARCHAR(16)     NOT NULL DEFAULT 'api',  -- api/cli/schedule
    input        JSON            NULL,
    output       JSON            NULL,
    error        TEXT            NULL,
    started_at   DATETIME        NULL,
    finished_at  DATETIME        NULL,
    created_at   DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    UNIQUE KEY uk_run_id (run_id),
    KEY idx_run_wf (workflow_id),
    KEY idx_run_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS node_run (
    id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    run_id      VARCHAR(64)     NOT NULL,
    node_id     VARCHAR(96)     NOT NULL,
    node_type   VARCHAR(32)     NOT NULL,
    status      VARCHAR(16)     NOT NULL,                 -- pending/running/succeeded/failed/skipped
    input       JSON            NULL,
    output      JSON            NULL,
    error       TEXT            NULL,
    attempt     INT             NOT NULL DEFAULT 0,
    cost_ms     INT             NOT NULL DEFAULT 0,
    started_at  DATETIME        NULL,
    finished_at DATETIME        NULL,
    PRIMARY KEY (id),
    KEY idx_noderun_run (run_id, node_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS node_run;
DROP TABLE IF EXISTS workflow_run;
DROP TABLE IF EXISTS workflow_version;
DROP TABLE IF EXISTS workflow;
DROP TABLE IF EXISTS application;

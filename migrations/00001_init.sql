-- +goose Up
-- M0 初始 schema：仅建 project 表作为基线，验证迁移链路可跑通。
-- 完整表（workflow/workflow_version/workflow_run/node_run/application）在 M2 补齐。
CREATE TABLE IF NOT EXISTS project (
    id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    project_id  VARCHAR(64)     NOT NULL,
    name        VARCHAR(128)    NOT NULL,
    description VARCHAR(512)    NOT NULL DEFAULT '',
    created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at  DATETIME        NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_project_id (project_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS project;

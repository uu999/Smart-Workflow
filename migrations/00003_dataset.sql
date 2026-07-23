-- +goose Up
-- M10 dataset 表：评测集。row_data 整存 JSON 数组（每元素一条样本，如 {"query":"...","label":"..."}）。
-- 风格对齐 application 表：软删除 deleted_at、按 project 索引、app_id 式业务主键 dataset_id。
-- col_schema 记录行的字段定义（可选，供 dataset 节点端口推断），row_data 是真正的数据。
-- 注意：避开 MySQL 保留字（rows/schema），列名用 row_data / col_schema。

CREATE TABLE IF NOT EXISTS dataset (
    id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    dataset_id  VARCHAR(64)     NOT NULL,
    project_id  VARCHAR(64)     NOT NULL,
    name        VARCHAR(128)    NOT NULL,
    col_schema  JSON            NULL,                    -- 行字段定义（可选）：[{name,type}]
    row_data    JSON            NOT NULL,                -- 行集：JSON 数组，每元素一条样本
    row_count   INT             NOT NULL DEFAULT 0,      -- 行数（冗余，便于列表展示/校验）
    status      TINYINT         NOT NULL DEFAULT 0,
    created_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME        NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at  DATETIME        NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_dataset_id (dataset_id),
    KEY idx_dataset_project (project_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- +goose Down
DROP TABLE IF EXISTS dataset;

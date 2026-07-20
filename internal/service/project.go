package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/smart-workflow/smart-workflow/internal/storage/mysql"
	"github.com/smart-workflow/smart-workflow/internal/storage/mysql/gen"
)

// ProjectService 封装项目的持久化操作（M6）。
type ProjectService struct {
	store *mysql.Store
}

func NewProjectService(store *mysql.Store) *ProjectService {
	return &ProjectService{store: store}
}

// Project 是对外的项目视图。
type Project struct {
	ProjectID   string `json:"project_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Create 新建项目，返回 project_id。
func (s *ProjectService) Create(ctx context.Context, name, description string) (string, error) {
	pid := genID("proj")
	if _, err := s.store.Q.CreateProject(ctx, gen.CreateProjectParams{
		ProjectID:   pid,
		Name:        name,
		Description: description,
	}); err != nil {
		return "", fmt.Errorf("create project: %w", err)
	}
	return pid, nil
}

// Get 读取项目。
func (s *ProjectService) Get(ctx context.Context, projectID string) (*Project, error) {
	row, err := s.store.Q.GetProjectByProjectID(ctx, projectID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &Project{ProjectID: row.ProjectID, Name: row.Name, Description: row.Description}, nil
}

// List 分页列出项目。
func (s *ProjectService) List(ctx context.Context, limit, offset int32) ([]Project, error) {
	rows, err := s.store.Q.ListProjects(ctx, gen.ListProjectsParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	out := make([]Project, 0, len(rows))
	for _, r := range rows {
		out = append(out, Project{ProjectID: r.ProjectID, Name: r.Name, Description: r.Description})
	}
	return out, nil
}

// Update 修改项目 name/description。
func (s *ProjectService) Update(ctx context.Context, projectID, name, description string) error {
	res, err := s.store.Q.UpdateProject(ctx, gen.UpdateProjectParams{
		Name:        name,
		Description: description,
		ProjectID:   projectID,
	})
	if err != nil {
		return fmt.Errorf("update project: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete 软删除项目。
func (s *ProjectService) Delete(ctx context.Context, projectID string) error {
	res, err := s.store.Q.SoftDeleteProject(ctx, projectID)
	if err != nil {
		return fmt.Errorf("delete project: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

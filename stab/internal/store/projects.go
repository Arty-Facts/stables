package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotFound is returned when a lookup finds no matching row.
var ErrNotFound = errors.New("not found")

// UpsertProject inserts or updates a project.
func (s *Store) UpsertProject(p Project) (Project, error) {
	now := Now()
	if p.CreatedAt == "" {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	_, err := s.db.Exec(`
		INSERT INTO projects (id, name, root_path, image, profile, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, root_path=excluded.root_path, image=excluded.image,
			profile=excluded.profile, updated_at=excluded.updated_at`,
		p.ID, p.Name, p.RootPath, nullable(p.Image), nullable(p.Profile), p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return p, fmt.Errorf("store: upsert project: %w", err)
	}
	return p, nil
}

// GetProject returns a project by ID.
func (s *Store) GetProject(id string) (Project, error) {
	var p Project
	err := s.db.QueryRow(`
		SELECT id, name, root_path, COALESCE(image,''), COALESCE(profile,''), created_at, updated_at
		FROM projects WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &p.RootPath, &p.Image, &p.Profile, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	if err != nil {
		return p, err
	}
	return p, nil
}

// ListProjects returns all projects.
func (s *Store) ListProjects() ([]Project, error) {
	rows, err := s.db.Query(`SELECT id FROM projects ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(ids))
	for _, id := range ids {
		p, err := s.GetProject(id)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

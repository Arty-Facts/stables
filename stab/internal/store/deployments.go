package store

import (
	"database/sql"
	"errors"
	"fmt"
)

// KindLocal is the deployment kind for the local controller.
const KindLocal = "local"

// UpsertDeployment inserts or updates a deployment.
func (s *Store) UpsertDeployment(d Deployment) (Deployment, error) {
	now := Now()
	if d.CreatedAt == "" {
		d.CreatedAt = now
	}
	d.UpdatedAt = now
	if d.Kind == "" {
		d.Kind = KindLocal
	}
	if d.DesiredState == "" {
		d.DesiredState = "pending"
	}
	if d.ObservedState == "" {
		d.ObservedState = "pending"
	}
	_, err := s.db.Exec(`
		INSERT INTO deployments (id, project_id, kind, container_name,
			tmux_session, image, desired_state, observed_state, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			project_id=excluded.project_id, kind=excluded.kind,
			container_name=excluded.container_name, tmux_session=excluded.tmux_session,
			image=excluded.image, desired_state=excluded.desired_state,
			observed_state=excluded.observed_state, updated_at=excluded.updated_at`,
		d.ID, d.ProjectID, d.Kind, nullable(d.ContainerName),
		nullable(d.TmuxSession), nullable(d.Image), d.DesiredState, d.ObservedState,
		d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return d, fmt.Errorf("store: upsert deployment: %w", err)
	}
	return d, nil
}

// GetDeployment returns a deployment by ID.
func (s *Store) GetDeployment(id string) (Deployment, error) {
	var d Deployment
	err := s.db.QueryRow(`
		SELECT id, project_id, kind, COALESCE(container_name,''),
			COALESCE(tmux_session,''), COALESCE(image,''), desired_state, observed_state, created_at, updated_at
		FROM deployments WHERE id = ?`, id).
		Scan(&d.ID, &d.ProjectID, &d.Kind, &d.ContainerName,
			&d.TmuxSession, &d.Image, &d.DesiredState, &d.ObservedState, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	if err != nil {
		return d, err
	}
	return d, nil
}

// ListDeployments returns all deployments, optionally filtered by project.
func (s *Store) ListDeployments(projectID string) ([]Deployment, error) {
	q := `SELECT id FROM deployments`
	args := []any{}
	if projectID != "" {
		q += ` WHERE project_id = ?`
		args = append(args, projectID)
	}
	q += ` ORDER BY created_at`
	rows, err := s.db.Query(q, args...)
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
	out := make([]Deployment, 0, len(ids))
	for _, id := range ids {
		d, err := s.GetDeployment(id)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// UpsertContainer inserts or updates a container row.
func (s *Store) UpsertContainer(c Container) (Container, error) {
	now := Now()
	if c.CreatedAt == "" {
		c.CreatedAt = now
	}
	c.UpdatedAt = now
	_, err := s.db.Exec(`
		INSERT INTO containers (id, deployment_id, name, status, created_at, updated_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			deployment_id=excluded.deployment_id, name=excluded.name,
			status=excluded.status, updated_at=excluded.updated_at`,
		c.ID, c.DeploymentID, c.Name, nullable(c.Status), c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return c, fmt.Errorf("store: upsert container: %w", err)
	}
	return c, nil
}

// UpsertTmuxSession inserts or updates a tmux session row.
func (s *Store) UpsertTmuxSession(t TmuxSession) (TmuxSession, error) {
	now := Now()
	if t.CreatedAt == "" {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	_, err := s.db.Exec(`
		INSERT INTO tmux_sessions (id, deployment_id, name, windows, created_at, updated_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			deployment_id=excluded.deployment_id, name=excluded.name,
			windows=excluded.windows, updated_at=excluded.updated_at`,
		t.ID, t.DeploymentID, t.Name, nullable(t.Windows), t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return t, fmt.Errorf("store: upsert tmux session: %w", err)
	}
	return t, nil
}

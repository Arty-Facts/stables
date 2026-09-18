package store

import "time"

// Now returns the current time in UTC as an RFC3339 string, the canonical
// timestamp format used throughout the store.
func Now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// Project maps a canonical local path to a stable ID.
type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	RootPath  string `json:"root_path"`
	Image     string `json:"image,omitempty"`
	Profile   string `json:"profile,omitempty"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Deployment is a single project running in a local container. It carries the
// project's ID so future remote deployments can be added without changing the
// data model.
type Deployment struct {
	ID            string `json:"id"`
	ProjectID     string `json:"project_id"`
	Kind          string `json:"kind"` // "local"
	ContainerName string `json:"container_name,omitempty"`
	TmuxSession   string `json:"tmux_session,omitempty"`
	Image         string `json:"image,omitempty"`
	DesiredState  string `json:"desired_state"`
	ObservedState string `json:"observed_state"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// Container records a managed Docker container.
type Container struct {
	ID           string `json:"id"`
	DeploymentID string `json:"deployment_id"`
	Name         string `json:"name"`
	Status       string `json:"status,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// TmuxSession records a managed tmux session inside a container.
type TmuxSession struct {
	ID           string `json:"id"`
	DeploymentID string `json:"deployment_id"`
	Name         string `json:"name"`
	Windows      string `json:"windows,omitempty"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

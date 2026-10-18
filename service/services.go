package service

// Services bundles the app's backend services.
type Services struct {
	// Workspace is the absolute workspace root of the active project.
	Workspace string
}

// NewServices creates the service container.
func NewServices(workspace string) *Services {
	return &Services{Workspace: workspace}
}

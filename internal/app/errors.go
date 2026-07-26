package app

import "errors"

var (
	ErrConfigPathRequired       = errors.New("config path is required")
	ErrContextRequired          = errors.New("context is required")
	ErrDependenciesIncomplete   = errors.New("application dependencies are incomplete")
	ErrUnsupportedPlatform      = errors.New("unsupported platform")
	ErrInvalidReconcileInterval = errors.New("reconcile interval must be positive")
	ErrReconcileRunnerActive    = errors.New("reconcile runner is already active")
	ErrApplicationClosed        = errors.New("application is closed")
)

// ReconcileCode is deliberately a closed, credential-free classification.
type ReconcileCode string

const (
	ReconcileOK                 ReconcileCode = "ok"
	ReconcileUpstreamFailure    ReconcileCode = "upstream_failure"
	ReconcileUpstreamTimeout    ReconcileCode = "upstream_timeout"
	ReconcileIncompatibleTarget ReconcileCode = "incompatible_target"
)

// ReconcileResult contains no platform response or wrapped error text.
type ReconcileResult struct {
	TargetID string        `json:"target_id"`
	Code     ReconcileCode `json:"code"`
}

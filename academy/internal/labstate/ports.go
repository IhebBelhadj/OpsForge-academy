package labstate

import (
	"context"

	"github.com/IhebBelhadj/opsforge-academy/academy/internal/lab"
)

// LabController reports the lab's state (opsforgeapi.Client).
type LabController interface {
	State(ctx context.Context) (lab.State, error)
}

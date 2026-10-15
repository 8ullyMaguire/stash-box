package api

import (
	"context"

	"github.com/stashapp/stash-box/internal/models"
)

// InstanceMetrics serves this instance's counters (spec section 16.2).
//
// THE MAPPING IS THE WHOLE RESOLVER, and that is the point: the definitions live in the metrics service beside
// the accessors that read the columns, so this file cannot invent a figure or restate one. Access is the
// schema's business (`@hasRole(role: ADMIN)` on the field), not a check here, because a check here would be a
// second place the rule lives.
func (r *queryResolver) InstanceMetrics(ctx context.Context) (*models.InstanceMetrics, error) {
	result, err := r.services.Metrics().InstanceMetrics(ctx)
	if err != nil {
		return nil, err
	}

	figures := make([]models.InstanceMetric, 0, len(result.Metrics))
	for _, m := range result.Metrics {
		figures = append(figures, models.InstanceMetric{
			Key:        m.Key,
			Unit:       m.Unit,
			Definition: m.Definition,
			Value:      int(m.Value),
		})
	}

	return &models.InstanceMetrics{
		GeneratedAt: result.GeneratedAt,
		Since:       result.Since,
		WindowDays:  result.WindowDays,
		Metrics:     figures,
	}, nil
}

package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mizuchilabs/inkan/internal/db"
)

type StatsInput struct {
	Days int `query:"days" minimum:"1" maximum:"3650" default:"90" doc:"How many days to look back"`
}

// Snapshot is the count of one day for one plan of a product.
type Snapshot struct {
	Day       string `json:"day"        doc:"YYYY-MM-DD in UTC"`
	ProductID int64  `json:"product_id"`
	Plan      string `json:"plan"`
	Active    int64  `json:"active"     doc:"Licenses that had not run out"`
}

type StatsOutput struct {
	Body []Snapshot
}

func registerStats(api huma.API, q *db.Queries) {
	huma.Register(api, huma.Operation{
		OperationID: "list-stats",
		Method:      http.MethodGet,
		Path:        "/api/stats",
		Summary:     "Show how the license counts moved",
		Description: "One entry per day, product and plan, oldest first. A plan without licenses on a day has no entry.",
		Tags:        []string{"Licenses"},
	}, func(ctx context.Context, in *StatsInput) (*StatsOutput, error) {
		since := time.Now().UTC().AddDate(0, 0, -in.Days).Format(time.DateOnly)
		rows, err := q.ListSnapshots(ctx, since)
		if err != nil {
			return nil, huma.Error500InternalServerError("failed to list snapshots")
		}
		out := make([]Snapshot, len(rows))
		for i, r := range rows {
			out[i] = Snapshot{Day: r.Day, ProductID: r.ProductID, Plan: r.Plan, Active: r.Active}
		}
		return &StatsOutput{Body: out}, nil
	})
}

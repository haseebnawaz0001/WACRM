package deals

import (
	"fmt"

	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/models"
)

// RegisterFields adds deal-based filters to a contact query registry
// (plan 07 + plan 00 F6).
//
// These are the questions a pipeline makes worth asking about a *contact*:
// "everyone with an open deal in Negotiation", "everyone we lost this quarter".
// They compile to EXISTS subqueries rather than joins, because a contact with
// four deals must not appear four times in a segment.
func RegisterFields(r *contactquery.Registry, stages []models.PipelineStage, pipelines []models.Pipeline) {
	stageOptions := make([]contactquery.Option, 0, len(stages))
	for _, stage := range stages {
		stageOptions = append(stageOptions, contactquery.Option{
			Value: stage.ID.String(), Label: stage.Name,
		})
	}
	pipelineOptions := make([]contactquery.Option, 0, len(pipelines))
	for _, pipeline := range pipelines {
		pipelineOptions = append(pipelineOptions, contactquery.Option{
			Value: pipeline.ID.String(), Label: pipeline.Name,
		})
	}

	r.Register(contactquery.Field{
		Key:      "deal.has_open",
		LabelKey: "filters.fields.deal_has_open",
		Type:     contactquery.TypeBoolean,
		Build:    existsBuild(func() (string, []any) { return "d.status = ?", []any{models.DealOpen} }),
	})

	r.Register(contactquery.Field{
		Key:      "deal.stage_id",
		LabelKey: "filters.fields.deal_stage",
		Type:     contactquery.TypeOption,
		Options:  stageOptions,
		Build:    dealOptionBuild("d.stage_id"),
	})

	r.Register(contactquery.Field{
		Key:      "deal.pipeline_id",
		LabelKey: "filters.fields.deal_pipeline",
		Type:     contactquery.TypeOption,
		Options:  pipelineOptions,
		Build:    dealOptionBuild("d.pipeline_id"),
	})

	r.Register(contactquery.Field{
		Key:      "deal.status",
		LabelKey: "filters.fields.deal_status",
		Type:     contactquery.TypeOption,
		Options: []contactquery.Option{
			{Value: models.DealOpen, Label: "Open"},
			{Value: models.DealWon, Label: "Won"},
			{Value: models.DealLost, Label: "Lost"},
		},
		Build: dealOptionBuild("d.status"),
	})
}

// dealExists wraps a condition on deals as an EXISTS against the contact.
func dealExists(condition string) string {
	return fmt.Sprintf(`EXISTS (
		SELECT 1 FROM deals d
		WHERE d.contact_id = contacts.id
		  AND d.deleted_at IS NULL
		  AND %s)`, condition)
}

// existsBuild compiles a boolean "do they have one" field.
func existsBuild(condition func() (string, []any)) contactquery.BuildFunc {
	return func(_ contactquery.Field, rule contactquery.Node, _ contactquery.Viewer) (string, []any, error) {
		sql, args := condition()
		exists := dealExists(sql)
		if rule.Operator == contactquery.OpIsFalse {
			return "NOT " + exists, args, nil
		}
		return exists, args, nil
	}
}

// dealOptionBuild compiles an in/not_in rule against one deal column.
func dealOptionBuild(column string) contactquery.BuildFunc {
	return func(f contactquery.Field, rule contactquery.Node, v contactquery.Viewer) (string, []any, error) {
		// Compile the leaf against a proxy so the operator semantics stay in
		// one place; only the EXISTS wrapper is specific to deals.
		proxy := contactquery.Field{Key: f.Key, Type: contactquery.TypeOption, Column: column}

		negate := rule.Operator == contactquery.OpNotIn
		if negate {
			rule.Operator = contactquery.OpIn
		}

		inner, err := contactquery.CompileLeaf(proxy, rule, v)
		if err != nil {
			return "", nil, err
		}

		exists := dealExists(inner.SQL)
		if negate {
			// "not in stage X" must mean "has no deal in stage X", not "has
			// some other deal": negating inside the subquery would match any
			// contact with a second deal anywhere else.
			return "NOT " + exists, inner.Args, nil
		}
		return exists, inner.Args, nil
	}
}

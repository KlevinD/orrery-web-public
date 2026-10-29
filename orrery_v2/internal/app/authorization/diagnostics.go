package authorization

type EnforceTransitionDiagnostics struct {
	Ready             bool
	BackfillValidated bool
	Shadow            ShadowDecisionStats
	Backfill          RelationBackfillResult
}

func NewEnforceTransitionDiagnostics(shadow ShadowDecisionStats, backfill RelationBackfillResult) EnforceTransitionDiagnostics {
	backfillValidated := !backfill.Disabled &&
		backfill.Diff.Errors == 0 &&
		backfill.Diff.Missing == 0 &&
		backfill.Diff.Extra == 0 &&
		backfill.Diff.Checked >= backfill.Tuples
	shadowClean := shadow.Mismatches == 0 && shadow.PrimaryErrors == 0 && shadow.ShadowErrors == 0
	return EnforceTransitionDiagnostics{
		Ready:             backfillValidated && shadowClean,
		BackfillValidated: backfillValidated,
		Shadow:            shadow,
		Backfill:          backfill,
	}
}

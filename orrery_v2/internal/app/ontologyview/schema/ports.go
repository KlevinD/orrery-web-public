package schema

import (
	"context"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/ontology"
)

type RegistryReader interface {
	ListRegistryDefinitions(context.Context, ontology.RegistryDefinitionQuery) (ontology.RegistryDefinitionPage, error)
	GetRegistryDefinition(context.Context, ontology.RegistryDefinitionKind, string) (ontology.RegistryDefinition, error)
	GetRegistryDefinitions(context.Context, ontology.RegistryDefinitionKind, []string) ([]ontology.RegistryDefinition, error)
}

type ActionReader interface {
	ListActionTypePage(context.Context, string, string, int) (actioncontract.ActionTypePage, error)
	GetActionType(context.Context, actioncontract.ActionTypeID) (actioncontract.ActionType, error)
}

type VisibilityPolicy interface {
	Evaluate(context.Context, datavisibility.EvaluateRequest) (datavisibility.Decision, error)
}

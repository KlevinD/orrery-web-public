package ontology

import "context"

type Registry interface {
	SaveObjectType(context.Context, ObjectType) error
	GetObjectType(context.Context, string) (ObjectType, error)
	ListObjectTypes(context.Context) ([]ObjectType, error)
	HasObjectType(context.Context, string) (bool, error)
	SaveLinkType(context.Context, LinkType) error
	GetLinkType(context.Context, string) (LinkType, error)
	ListLinkTypes(context.Context) ([]LinkType, error)
	HasLinkType(context.Context, string) (bool, error)
	SavePropertyType(context.Context, PropertyType) error
	GetPropertyType(context.Context, string) (PropertyType, error)
	ListPropertyTypes(context.Context, string) ([]PropertyType, error)
	SaveMappingRule(context.Context, MappingRule) error
	ListMappingRules(context.Context, string) ([]MappingRule, error)
}

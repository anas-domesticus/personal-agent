package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
)

// CreateNodeType persists a dynamic node type. Dynamic nodes always retain
// immutable versions; unlike static Go registration, this remains available
// after River has frozen the in-process handler registry.
func (g *Graph) CreateNodeType(ctx context.Context, kind string, dataSchema json.RawMessage) (domain.KnowledgeNodeType, error) {
	if !kindPattern.MatchString(kind) {
		return domain.KnowledgeNodeType{}, fmt.Errorf("node kind %q must match %s", kind, kindPattern)
	}
	if _, err := compileSchema(kind, dataSchema); err != nil {
		return domain.KnowledgeNodeType{}, err
	}
	var out domain.KnowledgeNodeType
	err := g.store.InTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		row, err := q.InsertGraphNodeType(ctx, db.InsertGraphNodeTypeParams{Kind: kind, Versioned: true, DataSchema: dataSchema})
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("node kind %q: %w", kind, domain.ErrExists)
		}
		if err != nil {
			return graphError(err, "create node type")
		}
		out = domain.KnowledgeNodeType{
			Kind: row.Kind, Versioned: row.Versioned, DataSchema: row.DataSchema,
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
		return nil
	})
	return out, err
}

// GetNodeType reads a persisted static or dynamic node definition.
func (g *Graph) GetNodeType(ctx context.Context, kind string) (domain.KnowledgeNodeType, error) {
	row, err := g.store.GetGraphNodeType(ctx, kind)
	if err != nil {
		return domain.KnowledgeNodeType{}, graphError(err, "graph node type")
	}
	return domain.KnowledgeNodeType{
		Kind: row.Kind, Versioned: row.Versioned, DataSchema: row.DataSchema,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

// ListNodeTypes returns every persisted node definition ordered by kind.
func (g *Graph) ListNodeTypes(ctx context.Context) ([]domain.KnowledgeNodeType, error) {
	rows, err := g.store.ListGraphNodeTypes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list graph node types: %w", err)
	}
	out := make([]domain.KnowledgeNodeType, len(rows))
	for i, row := range rows {
		out[i] = knowledgeNodeType(row)
	}
	return out, nil
}

func knowledgeNodeType(row db.GraphNodeType) domain.KnowledgeNodeType {
	return domain.KnowledgeNodeType{
		Kind: row.Kind, Versioned: row.Versioned, DataSchema: row.DataSchema,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

// CreateEdgeType persists a dynamic edge schema and its complete endpoint set.
func (g *Graph) CreateEdgeType(ctx context.Context, kind string, dataSchema json.RawMessage, endpoints []Endpoint) (domain.KnowledgeEdgeType, error) {
	if !kindPattern.MatchString(kind) {
		return domain.KnowledgeEdgeType{}, fmt.Errorf("edge kind %q must match %s", kind, kindPattern)
	}
	if _, err := compileSchema(kind, dataSchema); err != nil {
		return domain.KnowledgeEdgeType{}, err
	}
	if len(endpoints) == 0 {
		return domain.KnowledgeEdgeType{}, errors.New("edge must permit at least one endpoint combination")
	}
	endpoints = append([]Endpoint(nil), endpoints...)
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].Source == endpoints[j].Source {
			return endpoints[i].Target < endpoints[j].Target
		}
		return endpoints[i].Source < endpoints[j].Source
	})
	seen := make(map[Endpoint]bool, len(endpoints))
	for _, endpoint := range endpoints {
		if !kindPattern.MatchString(endpoint.Source) || !kindPattern.MatchString(endpoint.Target) {
			return domain.KnowledgeEdgeType{}, fmt.Errorf("edge endpoint %q to %q has an invalid node kind", endpoint.Source, endpoint.Target)
		}
		if seen[endpoint] {
			return domain.KnowledgeEdgeType{}, fmt.Errorf("duplicate edge endpoint %q to %q", endpoint.Source, endpoint.Target)
		}
		seen[endpoint] = true
	}

	var out domain.KnowledgeEdgeType
	err := g.store.InTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		row, err := q.InsertGraphEdgeType(ctx, db.InsertGraphEdgeTypeParams{Kind: kind, DataSchema: dataSchema})
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("edge kind %q: %w", kind, domain.ErrExists)
		}
		if err != nil {
			return graphError(err, "create edge type")
		}
		for _, endpoint := range endpoints {
			if err := q.InsertGraphEdgeEndpoint(ctx, db.InsertGraphEdgeEndpointParams{
				EdgeKind: kind, SourceKind: endpoint.Source, TargetKind: endpoint.Target,
			}); err != nil {
				return graphError(err, "create edge endpoint")
			}
		}
		out = domain.KnowledgeEdgeType{
			Kind: row.Kind, DataSchema: row.DataSchema, Endpoints: domainEndpoints(endpoints),
			CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		}
		return nil
	})
	return out, err
}

// GetEdgeType reads a persisted static or dynamic edge definition.
func (g *Graph) GetEdgeType(ctx context.Context, kind string) (domain.KnowledgeEdgeType, error) {
	row, err := g.store.GetGraphEdgeType(ctx, kind)
	if err != nil {
		return domain.KnowledgeEdgeType{}, graphError(err, "graph edge type")
	}
	endpointRows, err := g.store.ListGraphEdgeEndpoints(ctx, kind)
	if err != nil {
		return domain.KnowledgeEdgeType{}, fmt.Errorf("list graph edge endpoints: %w", err)
	}
	endpoints := make([]Endpoint, len(endpointRows))
	for i, endpoint := range endpointRows {
		endpoints[i] = Endpoint{Source: endpoint.SourceKind, Target: endpoint.TargetKind}
	}
	return domain.KnowledgeEdgeType{
		Kind: row.Kind, DataSchema: row.DataSchema, Endpoints: domainEndpoints(endpoints),
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}, nil
}

// ListEdgeTypes returns every persisted edge definition ordered by kind.
func (g *Graph) ListEdgeTypes(ctx context.Context) ([]domain.KnowledgeEdgeType, error) {
	rows, err := g.store.ListGraphEdgeTypes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list graph edge types: %w", err)
	}
	out := make([]domain.KnowledgeEdgeType, len(rows))
	for i, row := range rows {
		definition, err := g.GetEdgeType(ctx, row.Kind)
		if err != nil {
			return nil, err
		}
		out[i] = definition
	}
	return out, nil
}

func domainEndpoints(endpoints []Endpoint) []domain.KnowledgeEndpoint {
	converted := make([]domain.KnowledgeEndpoint, len(endpoints))
	for i, endpoint := range endpoints {
		converted[i] = domain.KnowledgeEndpoint{Source: endpoint.Source, Target: endpoint.Target}
	}
	return converted
}

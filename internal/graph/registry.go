package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/google/uuid"
	"github.com/invopop/jsonschema"
	"github.com/jackc/pgx/v5"
	schemavalidator "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/anas-domesticus/personal-agent/internal/domain"
	"github.com/anas-domesticus/personal-agent/internal/store/db"
)

var kindPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

type validateData[T any] func(T, json.RawMessage) error

// RegisterNode registers an ordinary node whose current data is updated in
// place. Use RegisterVersionedNode to retain immutable snapshots.
func RegisterNode[T TextData](ctx context.Context, g *Graph, spec NodeSpec[T]) (*NodeRepository[T], error) {
	return registerNode(ctx, g, spec, false)
}

// RegisterVersionedNode registers a node with immutable version history.
func RegisterVersionedNode[T TextData](ctx context.Context, g *Graph, spec NodeSpec[T]) (*VersionedNodeRepository[T], error) {
	repository, err := registerNode(ctx, g, spec, true)
	if err != nil {
		return nil, err
	}
	return &VersionedNodeRepository[T]{NodeRepository: repository}, nil
}

func registerNode[T TextData](ctx context.Context, g *Graph, spec NodeSpec[T], versioned bool) (*NodeRepository[T], error) {
	definition, validate, err := nodeDefinition(spec.Kind, spec.Validate, versioned)
	if err != nil {
		return nil, err
	}

	g.mu.RLock()
	frozen := g.frozen
	_, exists := g.nodes[spec.Kind]
	g.mu.RUnlock()
	if frozen {
		return nil, errors.New("graph registry is frozen")
	}
	if exists {
		return nil, fmt.Errorf("node kind %q is already registered", spec.Kind)
	}

	registered := registeredNode{definition: definition}
	if len(spec.Enrichers) > 0 {
		registered.enrich = func(ctx context.Context, node Node[json.RawMessage]) error {
			data, err := decode[T](node.Data)
			if err != nil {
				return err
			}
			typed := Node[T]{Ref: node.Ref, Data: data, Revision: node.Revision, CreatedAt: node.CreatedAt, UpdatedAt: node.UpdatedAt}
			for _, enrich := range spec.Enrichers {
				if err := enrich(ctx, typed); err != nil {
					return err
				}
			}
			return nil
		}
	}

	if err := g.store.InTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		if _, err := q.InsertGraphNodeType(ctx, db.InsertGraphNodeTypeParams{
			Kind: definition.Kind, Versioned: definition.Versioned, DataSchema: definition.DataSchema,
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("register node type: %w", err)
		}
		stored, err := q.GetGraphNodeType(ctx, definition.Kind)
		if err != nil {
			return fmt.Errorf("read node type: %w", err)
		}
		if stored.Versioned != definition.Versioned || !equalJSON(stored.DataSchema, definition.DataSchema) {
			return fmt.Errorf("node kind %q differs from its persisted definition", definition.Kind)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.frozen {
		return nil, errors.New("graph registry is frozen")
	}
	if _, exists := g.nodes[spec.Kind]; exists {
		return nil, fmt.Errorf("node kind %q is already registered", spec.Kind)
	}
	repository := &NodeRepository[T]{graph: g, kind: spec.Kind, validate: validate}
	registered.upsert = func(ctx context.Context, id uuid.UUID, raw json.RawMessage, opts NodeWriteOptions) (domain.KnowledgeNode, error) {
		data, err := decodeStrict[T](raw)
		if err != nil {
			return domain.KnowledgeNode{}, err
		}
		node, err := repository.Upsert(ctx, id, data, opts)
		if err != nil {
			return domain.KnowledgeNode{}, err
		}
		return g.GetNode(ctx, spec.Kind, node.ID)
	}
	g.nodes[spec.Kind] = registered
	return repository, nil
}

func nodeDefinition[T TextData](kind string, validate func(T) error, versioned bool) (NodeType, validateData[T], error) {
	if !kindPattern.MatchString(kind) {
		return NodeType{}, nil, fmt.Errorf("node kind %q must match %s", kind, kindPattern)
	}
	schema := jsonschema.Reflect(new(T))
	raw, err := json.Marshal(schema)
	if err != nil {
		return NodeType{}, nil, fmt.Errorf("encode node schema: %w", err)
	}
	compiled, err := compileSchema(kind, raw)
	if err != nil {
		return NodeType{}, nil, err
	}
	check := func(value T, encoded json.RawMessage) error {
		if err := validateJSON(compiled, encoded); err != nil {
			return fmt.Errorf("node %q data: %w", kind, err)
		}
		if err := checkNodeText(value, encoded); err != nil {
			return fmt.Errorf("node %q data: %w", kind, err)
		}
		if validate != nil {
			if err := validate(value); err != nil {
				return fmt.Errorf("node %q data: %w", kind, err)
			}
		}
		return nil
	}
	return NodeType{Kind: kind, Versioned: versioned, DataSchema: raw}, check, nil
}

func checkNodeText[T TextData](value T, encoded json.RawMessage) error {
	var persisted struct {
		Title       *string `json:"title"`
		Description *string `json:"description"`
	}
	if err := json.Unmarshal(encoded, &persisted); err != nil {
		return fmt.Errorf("decode title and description: %w", err)
	}
	title, description := value.NodeText()
	if persisted.Title == nil || persisted.Description == nil || *persisted.Title != title || *persisted.Description != description {
		return errors.New("title and description must match NodeText")
	}
	return nil
}

// RegisterEdge registers an edge and its complete set of permitted endpoint
// combinations. Referenced node kinds must already be registered.
func RegisterEdge[T any](ctx context.Context, g *Graph, spec EdgeSpec[T]) (*EdgeRepository[T], error) {
	if !kindPattern.MatchString(spec.Kind) {
		return nil, fmt.Errorf("edge kind %q must match %s", spec.Kind, kindPattern)
	}
	if len(spec.Endpoints) == 0 {
		return nil, errors.New("edge must permit at least one endpoint combination")
	}
	schema := jsonschema.Reflect(new(T))
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode edge schema: %w", err)
	}
	compiled, err := compileSchema(spec.Kind, raw)
	if err != nil {
		return nil, err
	}

	endpoints := append([]Endpoint(nil), spec.Endpoints...)
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].Source == endpoints[j].Source {
			return endpoints[i].Target < endpoints[j].Target
		}
		return endpoints[i].Source < endpoints[j].Source
	})
	seen := map[Endpoint]bool{}
	permitted := map[Endpoint]bool{}
	for _, endpoint := range endpoints {
		if seen[endpoint] {
			return nil, fmt.Errorf("duplicate edge endpoint %q to %q", endpoint.Source, endpoint.Target)
		}
		seen[endpoint] = true
		permitted[endpoint] = true
	}
	g.mu.RLock()
	frozen := g.frozen
	_, exists := g.edges[spec.Kind]
	var endpointErr error
	if !frozen && !exists {
		for _, endpoint := range endpoints {
			if _, ok := g.nodes[endpoint.Source]; !ok {
				endpointErr = fmt.Errorf("edge source node kind %q is not registered", endpoint.Source)
				break
			}
			if _, ok := g.nodes[endpoint.Target]; !ok {
				endpointErr = fmt.Errorf("edge target node kind %q is not registered", endpoint.Target)
				break
			}
		}
	}
	g.mu.RUnlock()
	if frozen {
		return nil, errors.New("graph registry is frozen")
	}
	if exists {
		return nil, fmt.Errorf("edge kind %q is already registered", spec.Kind)
	}
	if endpointErr != nil {
		return nil, endpointErr
	}
	definition := EdgeType{Kind: spec.Kind, DataSchema: raw, Endpoints: endpoints}

	if err := g.store.InTx(ctx, func(q *db.Queries, _ pgx.Tx) error {
		if _, err := q.InsertGraphEdgeType(ctx, db.InsertGraphEdgeTypeParams{Kind: spec.Kind, DataSchema: raw}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("register edge type: %w", err)
		}
		stored, err := q.GetGraphEdgeType(ctx, spec.Kind)
		if err != nil {
			return fmt.Errorf("read edge type: %w", err)
		}
		if !equalJSON(stored.DataSchema, raw) {
			return fmt.Errorf("edge kind %q differs from its persisted definition", spec.Kind)
		}
		for _, endpoint := range endpoints {
			if err := q.InsertGraphEdgeEndpoint(ctx, db.InsertGraphEdgeEndpointParams{
				EdgeKind: spec.Kind, SourceKind: endpoint.Source, TargetKind: endpoint.Target,
			}); err != nil {
				return fmt.Errorf("register edge endpoint: %w", err)
			}
		}
		rows, err := q.ListGraphEdgeEndpoints(ctx, spec.Kind)
		if err != nil {
			return fmt.Errorf("read edge endpoints: %w", err)
		}
		if len(rows) != len(endpoints) {
			return fmt.Errorf("edge kind %q endpoint set differs from its persisted definition", spec.Kind)
		}
		for i, row := range rows {
			if row.SourceKind != endpoints[i].Source || row.TargetKind != endpoints[i].Target {
				return fmt.Errorf("edge kind %q endpoint set differs from its persisted definition", spec.Kind)
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	validate := func(value T, encoded json.RawMessage) error {
		if err := validateJSON(compiled, encoded); err != nil {
			return fmt.Errorf("edge %q data: %w", spec.Kind, err)
		}
		if spec.Validate != nil {
			if err := spec.Validate(value); err != nil {
				return fmt.Errorf("edge %q data: %w", spec.Kind, err)
			}
		}
		return nil
	}
	repository := &EdgeRepository[T]{graph: g, kind: spec.Kind, permitted: permitted, validate: validate}
	registered := registeredEdge{definition: definition}
	if len(spec.Enrichers) > 0 {
		registered.enrich = func(ctx context.Context, edge Edge[json.RawMessage]) error {
			data, err := decode[T](edge.Data)
			if err != nil {
				return err
			}
			typed := Edge[T]{
				ID: edge.ID, Kind: edge.Kind, Source: edge.Source, Target: edge.Target,
				Data: data, Revision: edge.Revision, CreatedAt: edge.CreatedAt, UpdatedAt: edge.UpdatedAt,
			}
			for _, enrich := range spec.Enrichers {
				if err := enrich(ctx, typed); err != nil {
					return err
				}
			}
			return nil
		}
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.frozen {
		return nil, errors.New("graph registry is frozen")
	}
	if _, exists := g.edges[spec.Kind]; exists {
		return nil, fmt.Errorf("edge kind %q is already registered", spec.Kind)
	}
	registered.upsert = func(ctx context.Context, source, target Ref, raw json.RawMessage, opts EdgeWriteOptions) (domain.KnowledgeEdge, error) {
		data, err := decodeStrict[T](raw)
		if err != nil {
			return domain.KnowledgeEdge{}, err
		}
		versioned, err := repository.UpsertVersion(ctx, source, target, data, opts)
		if err != nil {
			return domain.KnowledgeEdge{}, err
		}
		return g.GetEdge(ctx, spec.Kind, versioned.ID)
	}
	g.edges[spec.Kind] = registered
	return repository, nil
}

func compileSchema(kind string, raw json.RawMessage) (*schemavalidator.Schema, error) {
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("decode graph schema: %w", err)
	}
	if object, ok := document.(map[string]any); !ok || object == nil {
		return nil, errors.New("graph schema must be a JSON object")
	}
	compiler := schemavalidator.NewCompiler()
	if err := compiler.AddResource(kind+".json", document); err != nil {
		return nil, fmt.Errorf("load graph schema: %w", err)
	}
	compiled, err := compiler.Compile(kind + ".json")
	if err != nil {
		return nil, fmt.Errorf("compile graph schema: %w", err)
	}
	return compiled, nil
}

func validateJSON(schema *schemavalidator.Schema, raw json.RawMessage) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("decode graph data: %w", err)
	}
	if err := schema.Validate(value); err != nil {
		return fmt.Errorf("validate graph data: %w", err)
	}
	return nil
}

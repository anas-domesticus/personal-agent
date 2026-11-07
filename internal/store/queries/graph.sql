-- name: InsertGraphNodeType :one
insert into graph_node_types (kind, versioned, data_schema)
values ($1, $2, $3)
on conflict (kind) do nothing
returning kind, versioned, data_schema;

-- name: GetGraphNodeType :one
select kind, versioned, data_schema
from graph_node_types
where kind = $1;

-- name: InsertGraphEdgeType :one
insert into graph_edge_types (kind, data_schema)
values ($1, $2)
on conflict (kind) do nothing
returning kind, data_schema;

-- name: GetGraphEdgeType :one
select kind, data_schema
from graph_edge_types
where kind = $1;

-- name: InsertGraphEdgeEndpoint :exec
insert into graph_edge_type_endpoints (edge_kind, source_kind, target_kind)
values ($1, $2, $3)
on conflict do nothing;

-- name: ListGraphEdgeEndpoints :many
select edge_kind, source_kind, target_kind
from graph_edge_type_endpoints
where edge_kind = $1
order by source_kind, target_kind;

-- name: InsertGraphNode :one
insert into graph_nodes (id, kind, versioned, data, current_version)
values ($1, $2, $3, $4, $5)
returning *;

-- name: GetGraphNode :one
select *
from graph_nodes
where id = $1 and kind = $2;

-- name: GetGraphNodeForUpdate :one
select *
from graph_nodes
where id = $1 and kind = $2
for update;

-- name: UpdateGraphNodeData :one
update graph_nodes
set data = $3, revision = revision + 1, updated_at = now()
where id = $1 and kind = $2
returning *;

-- name: SetGraphNodeVersion :one
update graph_nodes
set current_version = $3, revision = revision + 1, updated_at = now()
where id = $1 and kind = $2
returning *;

-- name: DeleteGraphNode :execrows
delete from graph_nodes
where id = $1 and kind = $2;

-- name: InsertGraphNodeVersion :one
insert into graph_node_versions (node_id, version, data, metadata)
values ($1, $2, $3, $4)
returning *;

-- name: GetGraphNodeVersion :one
select *
from graph_node_versions
where node_id = $1 and version = $2;

-- name: GetGraphNodeMaxVersion :one
select coalesce(max(version), 0)::bigint
from graph_node_versions
where node_id = $1;

-- name: ListGraphNodeVersions :many
select *
from graph_node_versions
where node_id = $1 and ($2::bigint = 0 or version < $2)
order by version desc
limit $3;

-- name: InsertGraphEdge :one
insert into graph_edges (id, kind, source_id, source_kind, target_id, target_kind, data)
values ($1, $2, $3, $4, $5, $6, $7)
returning *;

-- name: GetGraphEdge :one
select *
from graph_edges
where id = $1 and kind = $2;

-- name: GetGraphEdgeBetween :one
select *
from graph_edges
where kind = $1 and source_id = $2 and target_id = $3;

-- name: GetGraphEdgeBetweenForUpdate :one
select *
from graph_edges
where kind = $1 and source_id = $2 and target_id = $3
for update;

-- name: UpdateGraphEdgeData :one
update graph_edges
set data = $4, revision = revision + 1, updated_at = now()
where id = $1 and kind = $2 and source_id = $3
returning *;

-- name: DeleteGraphEdge :execrows
delete from graph_edges
where id = $1 and kind = $2;

-- name: ListGraphEdgesFrom :many
select *
from graph_edges
where kind = $1 and source_id = $2
order by created_at, id;

-- name: ListGraphEdgesTo :many
select *
from graph_edges
where kind = $1 and target_id = $2
order by created_at, id;

-- +goose Up

alter table graph_nodes
    add column title_embedding vector(1536),
    add column description_embedding vector(1536),
    add column embedding_model text,
    add column embedding_revision bigint;

create index graph_nodes_title_embedding_idx on graph_nodes using hnsw (title_embedding vector_cosine_ops);
create index graph_nodes_description_embedding_idx on graph_nodes using hnsw (description_embedding vector_cosine_ops);

update graph_node_types
set data_schema = jsonb_set(data_schema, array['$defs', 'Objective', 'required'], '["title", "description", "success_criteria"]', false)
where kind = 'objective';

update graph_node_types
set data_schema = jsonb_set(
    jsonb_set(data_schema, array['$defs', 'Workflow', 'required'], '["title", "description", "graph"]', false),
    array['$defs', 'Workflow', 'properties'],
    ((data_schema #> array['$defs', 'Workflow', 'properties']) - 'name') ||
        jsonb_build_object('title', data_schema #> array['$defs', 'Workflow', 'properties', 'name']),
    false)
where kind = 'workflow';

update graph_node_types
set data_schema = jsonb_set(
    jsonb_set(data_schema, array['$defs', 'Claim', 'required'], '["title", "description"]', false),
    array['$defs', 'Claim', 'properties'],
    ((data_schema #> array['$defs', 'Claim', 'properties']) - 'text') ||
        jsonb_build_object('title', data_schema #> array['$defs', 'Claim', 'properties', 'text'], 'description', '{"type":"string"}'::jsonb),
    false)
where kind = 'claim';

update graph_node_types
set data_schema = jsonb_set(
    jsonb_set(data_schema, array['$defs', 'Source', 'required'], '["title", "description", "uri", "content_hash"]', false),
    array['$defs', 'Source', 'properties'],
    (data_schema #> array['$defs', 'Source', 'properties']) || '{"description":{"type":"string"}}',
    false)
where kind = 'source';

update graph_node_types
set data_schema = jsonb_set(
    jsonb_set(data_schema, array['$defs', 'Agent', 'required'], '["title", "description", "system_prompt", "model", "config", "notes"]', false),
    array['$defs', 'Agent', 'properties'],
    ((data_schema #> array['$defs', 'Agent', 'properties']) - 'name') ||
        jsonb_build_object('title', data_schema #> array['$defs', 'Agent', 'properties', 'name']),
    false)
where kind = 'agent';

alter table graph_node_versions disable trigger graph_node_versions_immutable;

update graph_nodes
set data = (data - 'name') || jsonb_build_object('title', data->'name')
where kind in ('workflow', 'agent') and data ? 'name';

update graph_nodes
set data = (data - 'text') || jsonb_build_object('title', data->'text', 'description', '""'::jsonb)
where kind = 'claim' and data ? 'text';

update graph_nodes
set data = jsonb_set(data, '{description}', '""'::jsonb, true)
where kind = 'source' and data is not null and not data ? 'description';

update graph_node_versions
set data = (data - 'name') || jsonb_build_object('title', data->'name')
where node_id in (select id from graph_nodes where kind in ('workflow', 'agent')) and data ? 'name';

update graph_node_versions
set data = (data - 'text') || jsonb_build_object('title', data->'text', 'description', '""'::jsonb)
where node_id in (select id from graph_nodes where kind = 'claim') and data ? 'text';

update graph_node_versions
set data = jsonb_set(data, '{description}', '""'::jsonb, true)
where node_id in (select id from graph_nodes where kind = 'source') and not data ? 'description';

alter table graph_node_versions enable trigger graph_node_versions_immutable;

-- +goose Down

alter table graph_node_versions disable trigger graph_node_versions_immutable;

update graph_nodes
set data = (data - 'title') || jsonb_build_object('name', data->'title')
where kind in ('workflow', 'agent') and data ? 'title';

update graph_nodes
set data = (data - 'title' - 'description') || jsonb_build_object('text', data->'title')
where kind = 'claim' and data ? 'title';

update graph_nodes
set data = data - 'description'
where kind = 'source' and data is not null;

update graph_node_versions
set data = (data - 'title') || jsonb_build_object('name', data->'title')
where node_id in (select id from graph_nodes where kind in ('workflow', 'agent')) and data ? 'title';

update graph_node_versions
set data = (data - 'title' - 'description') || jsonb_build_object('text', data->'title')
where node_id in (select id from graph_nodes where kind = 'claim') and data ? 'title';

update graph_node_versions
set data = data - 'description'
where node_id in (select id from graph_nodes where kind = 'source');

alter table graph_node_versions enable trigger graph_node_versions_immutable;

update graph_node_types
set data_schema = jsonb_set(data_schema, array['$defs', 'Objective', 'required'], '["title", "description", "success_criteria"]', false)
where kind = 'objective';

update graph_node_types
set data_schema = jsonb_set(
    jsonb_set(data_schema, array['$defs', 'Workflow', 'required'], '["name", "description", "graph"]', false),
    array['$defs', 'Workflow', 'properties'],
    ((data_schema #> array['$defs', 'Workflow', 'properties']) - 'title') ||
        jsonb_build_object('name', data_schema #> array['$defs', 'Workflow', 'properties', 'title']),
    false)
where kind = 'workflow';

update graph_node_types
set data_schema = jsonb_set(
    jsonb_set(data_schema, array['$defs', 'Claim', 'required'], '["text"]', false),
    array['$defs', 'Claim', 'properties'],
    ((data_schema #> array['$defs', 'Claim', 'properties']) - 'title' - 'description') ||
        jsonb_build_object('text', data_schema #> array['$defs', 'Claim', 'properties', 'title']),
    false)
where kind = 'claim';

update graph_node_types
set data_schema = jsonb_set(
    jsonb_set(data_schema, array['$defs', 'Source', 'required'], '["title", "uri", "content_hash"]', false),
    array['$defs', 'Source', 'properties'],
    (data_schema #> array['$defs', 'Source', 'properties']) - 'description',
    false)
where kind = 'source';

update graph_node_types
set data_schema = jsonb_set(
    jsonb_set(data_schema, array['$defs', 'Agent', 'required'], '["name", "description", "system_prompt", "model", "config", "notes"]', false),
    array['$defs', 'Agent', 'properties'],
    ((data_schema #> array['$defs', 'Agent', 'properties']) - 'title') ||
        jsonb_build_object('name', data_schema #> array['$defs', 'Agent', 'properties', 'title']),
    false)
where kind = 'agent';

drop index graph_nodes_description_embedding_idx;
drop index graph_nodes_title_embedding_idx;

alter table graph_nodes
    drop column embedding_revision,
    drop column embedding_model,
    drop column description_embedding,
    drop column title_embedding;

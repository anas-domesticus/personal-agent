-- name: CreateAgent :one
insert into agents (name, description)
values ($1, $2)
returning id, name, description, current_version, created_at, updated_at;

-- name: GetAgentByName :one
select id, name, description, current_version, created_at, updated_at
from agents
where name = $1;

-- name: ListAgents :many
select id, name, description, current_version, created_at, updated_at
from agents
order by name;

-- name: CreateAgentVersion :one
-- Appends the next version number for the agent.
insert into agent_versions (agent_id, version, system_prompt, model, tools, config, notes)
select sqlc.arg(agent_id), coalesce(max(version), 0) + 1,
       sqlc.arg(system_prompt), sqlc.arg(model), sqlc.arg(tools)::text[],
       sqlc.arg(config), sqlc.arg(notes)
from agent_versions
where agent_id = sqlc.arg(agent_id)
returning *;

-- name: SetAgentCurrentVersion :exec
update agents set current_version = $2 where id = $1;

-- name: GetAgentVersion :one
select * from agent_versions where agent_id = $1 and version = $2;

-- name: GetCurrentAgentVersion :one
select v.*
from agents a
join agent_versions v on v.agent_id = a.id and v.version = a.current_version
where a.name = $1;

-- name: ListAgentVersions :many
select * from agent_versions where agent_id = $1 order by version desc;

-- name: CreateRun :one
insert into runs (workflow_id, workflow_version, input, triggered_by)
values ($1, $2, $3, $4)
returning *;

-- name: GetRun :one
select r.*, w.name as workflow_name
from runs r
join workflows w on w.id = r.workflow_id
where r.id = $1;

-- name: ListRuns :many
-- Newest first. An empty workflow name lists runs of every workflow.
select r.id, r.workflow_version, r.status, r.error, r.triggered_by,
       r.created_at, r.started_at, r.finished_at, w.name as workflow_name
from runs r
join workflows w on w.id = r.workflow_id
where sqlc.arg(workflow_name)::text = '' or w.name = sqlc.arg(workflow_name)::text
order by r.created_at desc
limit sqlc.arg(lim)::int;

-- name: StartRun :execrows
-- Only a queued run can start, so a redelivered job can't run it twice.
update runs set status = 'running', started_at = now()
where id = $1 and status = 'queued';

-- name: FinishRun :exec
update runs
set status = $2, output = $3, error = $4, finished_at = now()
where id = $1;

-- name: CreateRunStep :one
insert into run_steps (run_id, node_id, agent_id, agent_version, input)
values ($1, $2, $3, $4, $5)
returning id;

-- name: FinishRunStep :exec
update run_steps
set status = $2, output = $3, error = $4, messages = $5,
    prompt_tokens = $6, completion_tokens = $7, finished_at = now()
where id = $1;

-- name: ListRunSteps :many
select s.*, a.name as agent_name
from run_steps s
left join agents a on a.id = s.agent_id
where s.run_id = $1
order by s.id;
